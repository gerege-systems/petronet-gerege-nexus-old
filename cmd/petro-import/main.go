/*
 * Gerege Nexus
 * Copyright (c) 2026 Gerege Systems Development Team, Gerege Nomadica Foundation
 * Distributed under the Apache 2.0 License.
 *
 * Loading a station list into one organisation.
 *
 * # What this is for, and what it is not
 *
 * It is a development seeder and a one-off onboarding tool. It is NOT how a
 * station register is meant to be kept: each company is a tenant on this
 * platform and maintains its own stations, which is the whole reason
 * petro_stations carries a tenant_id. A list scraped from OpenStreetMap is
 * good enough to build a map against and not good enough to ration fuel
 * against — it has no pump counts, no phone numbers, and prices that are
 * mostly null.
 *
 * So the tenant is named on the command line rather than guessed, and the
 * brand filter exists so the same file can be split across the companies it
 * actually describes:
 *
 *	go run ./cmd/petro-import -tenant demo -file stations.json
 *	go run ./cmd/petro-import -tenant petrovis -file stations.json -brand petrovis
 *
 * # Row-level security
 *
 * This opens its own pool with no dbguard on it, so it runs as the login role
 * and the tenant policies do not narrow what it writes. That is what lets it
 * write into a tenant nobody is signed in as. It is also why it must name the
 * tenant explicitly and refuses to run without one: nothing downstream of here
 * would catch a row written into the wrong organisation.
 */
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedStation is the shape of one record in the source file.
//
// Only the fields worth carrying are named. The source also holds crowd-report
// metadata — confidence scores, report counts, free-text notes — which belongs
// to a different system's idea of freshness and is not imported.
type seedStation struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Brand        string  `json:"brand"`
	BrandLabel   string  `json:"brand_label"`
	Lat          float64 `json:"lat"`
	Lon          float64 `json:"lon"`
	District     string  `json:"district"`
	Address      string  `json:"address"`
	OpeningHours string  `json:"opening_hours"`
	Status       string  `json:"status"`
	Fuels        map[string]struct {
		Status string   `json:"status"`
		Price  *float64 `json:"price"`
	} `json:"fuels"`
}

// Indicative pump prices, in tugrik, for the fuels the source names.
//
// Demo data, and labelled as such because it will be wrong the week after it is
// written: a price is the operator's to set, and the import writes these only
// where the source carries no price of its own. A station whose operator has
// not set a price is a station whose price nobody should trust, which is why
// these are here rather than left at zero — a zero would render as "free".
var demoPrices = map[string]struct {
	label string
	price float64
}{
	"ai80":         {"АИ-80", 2150},
	"ai92":         {"АИ-92", 2390},
	"ai95":         {"АИ-95", 3850},
	"ai98":         {"АИ-98", 4290},
	"diesel":       {"Дизель (ДТ)", 3690},
	"euro5_diesel": {"Euro-5 ДТ", 3890},
	"euro92":       {"Euro-92", 2590},
	"lpg":          {"Газ (LPG)", 1950},
}

// sourceName marks where these rows came from, so a re-import updates them
// rather than duplicating them and a later import from a real operator feed can
// be told apart from this one.
const sourceName = "osm-seed"

func main() {
	var (
		tenantSlug = flag.String("tenant", "", "slug of the organisation these stations belong to (required)")
		file       = flag.String("file", "", "path to the station JSON (required)")
		brand      = flag.String("brand", "", "import only this brand; empty imports every one")
		dryRun     = flag.Bool("dry-run", false, "report what would be written and write nothing")
		demoStock  = flag.Bool("demo-stock", false, "invent tank sizes and levels so the map has something to show")
	)
	flag.Parse()

	if *tenantSlug == "" || *file == "" {
		flag.Usage()
		os.Exit(2)
	}

	if err := run(*tenantSlug, *file, *brand, *dryRun, *demoStock); err != nil {
		fmt.Fprintln(os.Stderr, "petro-import:", err)
		os.Exit(1)
	}
}

func run(tenantSlug, file, brand string, dryRun, demoStock bool) error {
	raw, err := os.ReadFile(file) //nolint:gosec // an operator names the file
	if err != nil {
		return fmt.Errorf("read %s: %w", file, err)
	}
	var stations []seedStation
	if err := json.Unmarshal(raw, &stations); err != nil {
		return fmt.Errorf("parse %s: %w", file, err)
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://postgres:postgrespassword@localhost:5432/platform_db?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()

	var tenantID string
	err = pool.QueryRow(ctx,
		`SELECT id::text FROM registry.tenants WHERE slug = $1`, tenantSlug).Scan(&tenantID)
	if err != nil {
		return fmt.Errorf("no organisation with slug %q: %w", tenantSlug, err)
	}

	selected := stations[:0:0]
	for _, s := range stations {
		if brand != "" && s.Brand != brand {
			continue
		}
		if s.Lat == 0 && s.Lon == 0 {
			continue // a station with no location is not one this map can show
		}
		selected = append(selected, s)
	}

	fmt.Printf("organisation %s (%s): %d of %d stations selected\n",
		tenantSlug, tenantID, len(selected), len(stations))
	if dryRun {
		fmt.Println("dry run: nothing written")
		return nil
	}

	// One transaction. A half-loaded register is worse than none: the map would
	// render, look complete, and be missing whichever aimag the failure landed in.
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var stationRows, fuelRows int
	for _, s := range selected {
		var id string
		err := tx.QueryRow(ctx, `
			INSERT INTO workspace.petro_stations
			       (tenant_id, name, brand, brand_label, lat, lon,
			        aimag, district, address, opening_hours, status,
			        source, source_ref, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NOW())
			ON CONFLICT (tenant_id, source, source_ref)
			  WHERE source IS NOT NULL AND source_ref IS NOT NULL
			DO UPDATE SET name = EXCLUDED.name,
			              brand = EXCLUDED.brand,
			              brand_label = EXCLUDED.brand_label,
			              lat = EXCLUDED.lat,
			              lon = EXCLUDED.lon,
			              aimag = EXCLUDED.aimag,
			              district = EXCLUDED.district,
			              address = EXCLUDED.address,
			              opening_hours = EXCLUDED.opening_hours,
			              status = EXCLUDED.status,
			              updated_at = NOW()
			RETURNING id::text`,
			tenantID, s.Name, s.Brand, s.BrandLabel, s.Lat, s.Lon,
			aimagOf(s.District), districtOf(s.District), s.Address,
			defaultTo(s.OpeningHours, "24/7"),
			defaultTo(s.Status, "available"), sourceName, s.ID,
		).Scan(&id)
		if err != nil {
			return fmt.Errorf("station %s: %w", s.ID, err)
		}
		stationRows++

		// What this station sells. The source knows for 42 of the 500 — the rest
		// carry an empty map, because it is crowd-reported and most stations have
		// never been reported on. Under -demo-stock the rest get the three fuels
		// a Mongolian forecourt almost always has, so the map is worth looking at;
		// without it they get nothing, which is the truth.
		fuels := s.Fuels
		if len(fuels) == 0 && demoStock {
			fuels = map[string]struct {
				Status string   `json:"status"`
				Price  *float64 `json:"price"`
			}{
				"ai92":   {Status: "available"},
				"ai95":   {Status: "available"},
				"diesel": {Status: "available"},
			}
		}

		for code, fuel := range fuels {
			demo, known := demoPrices[code]
			if !known {
				continue // a fuel this deployment does not price is one it cannot sell
			}
			price := demo.price
			if fuel.Price != nil && *fuel.Price > 0 {
				price = *fuel.Price
			}
			// Tank size and level. Zero unless -demo-stock is given: an invented
			// level shown to a driver as fact is worse than no level at all, and
			// the API reports "unknown" for a tank with no size rather than
			// "empty" for exactly that reason.
			var capacity, level float64
			if demoStock {
				capacity, level = inventedStock(s.ID, code)
			}

			_, err := tx.Exec(ctx, `
				INSERT INTO workspace.petro_station_inventory
				       (station_id, tenant_id, fuel_type, fuel_label, price_mnt, status,
				        tank_capacity_liters, current_stock_liters, last_reported_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7::numeric, $8::numeric,
				        CASE WHEN $7::numeric > 0 THEN NOW() END)
				ON CONFLICT (station_id, fuel_type)
				DO UPDATE SET fuel_label = EXCLUDED.fuel_label,
				              price_mnt = EXCLUDED.price_mnt,
				              status = EXCLUDED.status,
				              tank_capacity_liters = CASE
				                  WHEN EXCLUDED.tank_capacity_liters > 0
				                  THEN EXCLUDED.tank_capacity_liters
				                  ELSE workspace.petro_station_inventory.tank_capacity_liters END,
				              current_stock_liters = CASE
				                  WHEN EXCLUDED.tank_capacity_liters > 0
				                  THEN EXCLUDED.current_stock_liters
				                  ELSE workspace.petro_station_inventory.current_stock_liters END`,
				id, tenantID, code, demo.label, price,
				defaultTo(fuel.Status, "available"), capacity, level)
			if err != nil {
				return fmt.Errorf("station %s fuel %s: %w", s.ID, code, err)
			}
			fuelRows++
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	fmt.Printf("wrote %d stations and %d fuel rows\n", stationRows, fuelRows)
	return nil
}

// inventedStock makes up a tank and a level for one fuel at one station.
//
// Deterministic, from a hash of the station and fuel, so the map does not
// reshuffle on every import and a screenshot stays reproducible. The spread —
// roughly 5% to 95% — is what makes the display worth looking at: a demo where
// every station is 70% full shows nothing a flat colour would not.
//
// This is scaffolding. Real levels come from an operator's own reading, and the
// day they do this flag stops being used.
func inventedStock(stationRef, fuelCode string) (capacity, level float64) {
	var h uint32 = 2166136261
	for _, b := range []byte(stationRef + ":" + fuelCode) {
		h = (h ^ uint32(b)) * 16777619
	}
	// 20,000 / 30,000 / 40,000 / 50,000 litres.
	capacity = float64(20000 + (h%4)*10000)
	// 5% .. 95% of it.
	fraction := 0.05 + float64((h>>8)%91)/100.0
	return capacity, capacity * fraction
}

func defaultTo(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// aimagOf and districtOf split the one place name the source file carries.
//
// The seed's `district` holds either a province ("Архангай аймаг") or a
// district of the capital ("Багахангай"), and the importer used to write it
// into BOTH columns — `VALUES (…, $7, $7, …)`. Every station in Ulaanbaatar
// then had a province called Bayangol, and the national table, which groups by
// province, produced a breakdown of a country that does not exist (audit §14).
//
// The rule is the country's own: a name ending in "аймаг" is a province, and
// anything else is a district of the capital. The suffix is dropped so the
// stored name is the register's own — «Архангай», as modules/petro.Aimags and
// the station form spell it — and a province body reading by name finds it.
func aimagOf(place string) string {
	place = strings.TrimSpace(place)
	if place == "" {
		return ""
	}
	if strings.HasSuffix(place, "аймаг") {
		return strings.TrimSpace(strings.TrimSuffix(place, "аймаг"))
	}
	return "Улаанбаатар"
}

func districtOf(place string) string {
	place = strings.TrimSpace(place)
	if strings.HasSuffix(place, "аймаг") {
		return ""
	}
	return place
}
