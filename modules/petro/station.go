/*
 * Gerege Nexus
 * Copyright (c) 2026 Gerege Systems Development Team, Gerege Nomadica Foundation
 * Distributed under the Apache 2.0 License.
 *
 * Keeping the station register: adding a forecourt, correcting one, retiring
 * one, and saying which grades it sells at what price.
 *
 * Until now the register could only be filled by cmd/petro-import, which says in
 * its own header that it is a seeding tool and not how a register is meant to
 * be kept. An operator signing in could read their stations and change nothing:
 * a station opened last week did not exist, a phone number that changed stayed
 * wrong, and a price — the number a citizen's entitlement is spent against —
 * was whatever the importer guessed months ago.
 *
 * # What this file will not let anybody do
 *
 * Set `current_stock_liters`. The rule the depot screen states (depot.go) holds
 * here for the same reason: litres in a tank are the sum of what went in and
 * what came out, and a box that overwrites that sum makes every receipt
 * beneath it advisory. Stock at a forecourt rises through
 * POST /trips/{id}/receive — a tanker unloading, against a delivery note — and
 * nowhere else.
 *
 * What an operator does own is the rest of the row: the price they charge, the
 * size of the vessel, and whether a grade is available at all. `status` is the
 * honest way to say "we are out": a statement about the forecourt, not an
 * accounting entry, and the thing a citizen actually needs to know.
 *
 * # Deleting
 *
 * Allowed only while nothing has happened at the station. Once a delivery has
 * been received there, the row is part of the chain a batch travelled and
 * deleting it would take that history with it — so the answer is 409 and the
 * advice to close the forecourt instead, which is what closing means.
 */

package petro

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gerege-systems/open-gerege-nexus/backend/pkg/nexus"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// StationDraft registers a forecourt.
//
// No stock and no queue: one is a consequence of deliveries and the other has
// no source yet (00001). A form that asked for either would be asking somebody
// to invent it.
type StationDraft struct {
	Name         string   `json:"name"`
	Brand        string   `json:"brand"`
	BrandLabel   string   `json:"brand_label"`
	Aimag        string   `json:"aimag"`
	District     string   `json:"district"`
	Address      string   `json:"address"`
	Phone        string   `json:"phone"`
	OpeningHours string   `json:"opening_hours"`
	Lat          *float64 `json:"lat"`
	Lon          *float64 `json:"lon"`
	TotalPumps   int      `json:"total_pumps"`
	ActivePumps  int      `json:"active_pumps"`
	VoucherOpen  *bool    `json:"is_voucher_enabled"`
}

// handleCreateStation adds a forecourt to this organisation's register.
func (m *Module) handleCreateStation(w http.ResponseWriter, r *http.Request) {
	claims, err := nexus.UserFromContext(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	tenantID, ok := nexus.RequireWorkspace(w, r)
	if !ok {
		return
	}

	var draft StationDraft
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&draft); err != nil {
		nexus.Error(w, http.StatusBadRequest, "invalid payload")
		return
	}
	draft.Name = strings.TrimSpace(draft.Name)
	if draft.Name == "" {
		nexus.Error(w, http.StatusBadRequest, "ШТС-ын нэр заавал шаардлагатай")
		return
	}
	// lat and lon are NOT NULL in the schema and a station with no coordinates
	// is one the map cannot draw. Zero is refused rather than stored: it is a
	// real place in the Gulf of Guinea, and a forecourt there reads as a bug in
	// the map rather than as a missing field.
	if draft.Lat == nil || draft.Lon == nil || (*draft.Lat == 0 && *draft.Lon == 0) {
		nexus.Error(w, http.StatusBadRequest, "байршил (өргөрөг, уртраг) заавал шаардлагатай")
		return
	}
	voucherOpen := true
	if draft.VoucherOpen != nil {
		voucherOpen = *draft.VoucherOpen
	}
	if draft.OpeningHours == "" {
		draft.OpeningHours = "24/7"
	}

	var station Station
	err = m.db.QueryRow(r.Context(), `
		INSERT INTO petro_stations
		       (tenant_id, name, brand, brand_label, aimag, district, address,
		        phone, opening_hours, lat, lon, total_pumps, active_pumps,
		        is_voucher_enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id::text, name, brand, brand_label, lat, lon, aimag, district,
		          address, phone, opening_hours, total_pumps, active_pumps,
		          current_queue_count, status, is_voucher_enabled, 0`,
		tenantID, draft.Name, draft.Brand, draft.BrandLabel, draft.Aimag, draft.District,
		draft.Address, draft.Phone, draft.OpeningHours, draft.Lat, draft.Lon,
		draft.TotalPumps, draft.ActivePumps, voucherOpen).
		Scan(&station.ID, &station.Name, &station.Brand, &station.BrandLabel,
			&station.Lat, &station.Lon, &station.Aimag, &station.District,
			&station.Address, &station.Phone, &station.OpeningHours,
			&station.TotalPumps, &station.ActivePumps, &station.QueueCount,
			&station.Status, &station.VoucherOpen, &station.FuelTypeCount)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "ШТС-ыг бүртгэж чадсангүй")
		return
	}

	nexus.Audit(r.Context(), tenantID, claims.UserID, "petro.station.created", station.ID,
		map[string]any{"name": station.Name, "aimag": station.Aimag})

	nexus.JSON(w, http.StatusCreated, station)
}

// StationPatch changes a forecourt. Every field is a pointer: a form that sends
// only what somebody edited must not blank the rest, and "" is a legitimate
// value for an address that turned out to be wrong.
type StationPatch struct {
	Name         *string  `json:"name"`
	Brand        *string  `json:"brand"`
	BrandLabel   *string  `json:"brand_label"`
	Aimag        *string  `json:"aimag"`
	District     *string  `json:"district"`
	Address      *string  `json:"address"`
	Phone        *string  `json:"phone"`
	OpeningHours *string  `json:"opening_hours"`
	Lat          *float64 `json:"lat"`
	Lon          *float64 `json:"lon"`
	TotalPumps   *int     `json:"total_pumps"`
	ActivePumps  *int     `json:"active_pumps"`
	Status       *string  `json:"status"`
	VoucherOpen  *bool    `json:"is_voucher_enabled"`
}

// handleUpdateStation corrects a row.
//
// COALESCE rather than a statement built from whichever fields arrived: the
// column list is fixed, so no name reaches the SQL from the request, and a
// field left out is written back to itself.
func (m *Module) handleUpdateStation(w http.ResponseWriter, r *http.Request) {
	claims, err := nexus.UserFromContext(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	tenantID, ok := nexus.RequireWorkspace(w, r)
	if !ok {
		return
	}

	var patch StationPatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&patch); err != nil {
		nexus.Error(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if patch.Name != nil && strings.TrimSpace(*patch.Name) == "" {
		nexus.Error(w, http.StatusBadRequest, "ШТС-ын нэр хоосон байж болохгүй")
		return
	}
	stationID := chi.URLParam(r, "id")
	if !isUUID(stationID) {
		nexus.Error(w, http.StatusBadRequest, "id буруу хэлбэртэй байна")
		return
	}

	var station Station
	err = m.db.QueryRow(r.Context(), `
		UPDATE petro_stations SET
		       name               = COALESCE($2, name),
		       brand              = COALESCE($3, brand),
		       brand_label        = COALESCE($4, brand_label),
		       aimag              = COALESCE($5, aimag),
		       district           = COALESCE($6, district),
		       address            = COALESCE($7, address),
		       phone              = COALESCE($8, phone),
		       opening_hours      = COALESCE($9, opening_hours),
		       lat                = COALESCE($10, lat),
		       lon                = COALESCE($11, lon),
		       total_pumps        = COALESCE($12, total_pumps),
		       active_pumps       = COALESCE($13, active_pumps),
		       status             = COALESCE($14, status),
		       is_voucher_enabled = COALESCE($15, is_voucher_enabled),
		       updated_at         = NOW()
		 WHERE id = $1
		RETURNING id::text, name, brand, brand_label, lat, lon, aimag, district,
		          address, phone, opening_hours, total_pumps, active_pumps,
		          current_queue_count, status, is_voucher_enabled,
		          (SELECT COUNT(*)::int FROM petro_station_inventory i
		            WHERE i.station_id = petro_stations.id)`,
		stationID, patch.Name, patch.Brand, patch.BrandLabel, patch.Aimag,
		patch.District, patch.Address, patch.Phone, patch.OpeningHours, patch.Lat, patch.Lon,
		patch.TotalPumps, patch.ActivePumps, patch.Status, patch.VoucherOpen).
		Scan(&station.ID, &station.Name, &station.Brand, &station.BrandLabel,
			&station.Lat, &station.Lon, &station.Aimag, &station.District,
			&station.Address, &station.Phone, &station.OpeningHours,
			&station.TotalPumps, &station.ActivePumps, &station.QueueCount,
			&station.Status, &station.VoucherOpen, &station.FuelTypeCount)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Not 403: a row this organisation may not see is a row that is not
		// there, and saying which of the two it is would answer a question the
		// caller has no right to ask.
		nexus.Error(w, http.StatusNotFound, "ШТС олдсонгүй")
		return
	case err != nil:
		nexus.Error(w, http.StatusInternalServerError, "өөрчлөлтийг хадгалж чадсангүй")
		return
	}

	nexus.Audit(r.Context(), tenantID, claims.UserID, "petro.station.updated", station.ID,
		map[string]any{"name": station.Name})

	nexus.JSON(w, http.StatusOK, station)
}

// handleDeleteStation removes a forecourt nothing has happened at yet.
//
// The guard is a query rather than a foreign key, because the foreign keys
// point the other way: petro_station_receipts cascades from here, so the
// database would happily delete the history with the station. A wrongly typed
// row is a mistake to undo; a forecourt that has taken deliveries is a record.
func (m *Module) handleDeleteStation(w http.ResponseWriter, r *http.Request) {
	claims, err := nexus.UserFromContext(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	tenantID, ok := nexus.RequireWorkspace(w, r)
	if !ok {
		return
	}
	stationID := chi.URLParam(r, "id")
	if !isUUID(stationID) {
		nexus.Error(w, http.StatusBadRequest, "id буруу хэлбэртэй байна")
		return
	}

	var receipts, trips int
	if err := m.db.QueryRow(r.Context(), `
		SELECT (SELECT COUNT(*)::int FROM petro_station_receipts WHERE station_id = $1),
		       (SELECT COUNT(*)::int FROM petro_dispatch_trips
		         WHERE to_station_id = $1 AND status <> 'cancelled')`,
		stationID).Scan(&receipts, &trips); err != nil {
		nexus.Error(w, http.StatusInternalServerError, "ШТС-ыг шалгаж чадсангүй")
		return
	}
	if receipts > 0 || trips > 0 {
		nexus.Error(w, http.StatusConflict,
			"энэ ШТС-д ачаа хүлээж авсан эсвэл рейс чиглэсэн байна — устгахын оронд төлөвийг нь хаалттай болго")
		return
	}

	tag, err := m.db.Exec(r.Context(), `DELETE FROM petro_stations WHERE id = $1`, stationID)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "ШТС-ыг устгаж чадсангүй")
		return
	}
	if tag.RowsAffected() == 0 {
		nexus.Error(w, http.StatusNotFound, "ШТС олдсонгүй")
		return
	}

	nexus.Audit(r.Context(), tenantID, claims.UserID, "petro.station.deleted", stationID, nil)

	w.WriteHeader(http.StatusNoContent)
}

// ─────────────────────────────────────────────────────────────────────────────
// What a forecourt sells
// ─────────────────────────────────────────────────────────────────────────────

// GradeDraft is one fuel grade at one station: what it costs, how big the
// vessel is, and whether it can be had.
//
// No litres. See the file header — the level is what receipts have put there.
type GradeDraft struct {
	FuelType       string   `json:"fuel_type"`
	FuelLabel      string   `json:"fuel_label"`
	PriceMNT       *float64 `json:"price_mnt"`
	CapacityLiters *float64 `json:"tank_capacity_liters"`
	Status         *string  `json:"status"`
}

// handleSetStationGrade adds a grade to a forecourt or changes its terms.
//
// An upsert, because the row has no identity of its own — its key is
// (station, fuel type), which the caller already knows. Registering АИ-92
// twice is the same statement of fact, not a duplicate.
//
// `last_reported_at` moves only when the status does. It answers "when did
// somebody last say something about this pump", which is what the console's
// stale counter reads; a price correction is not a report about availability.
func (m *Module) handleSetStationGrade(w http.ResponseWriter, r *http.Request) {
	claims, err := nexus.UserFromContext(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	tenantID, ok := nexus.RequireWorkspace(w, r)
	if !ok {
		return
	}

	var draft GradeDraft
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&draft); err != nil {
		nexus.Error(w, http.StatusBadRequest, "invalid payload")
		return
	}
	draft.FuelType = strings.TrimSpace(draft.FuelType)
	// The same check the tank and the consignment already make.
	//
	// This one only refused an empty string, so an operator could register a
	// grade spelled "АИ-92" or "euro92" and stock would accumulate against it.
	// The report form would then offer that row, and submitting it broke the
	// product foreign key — a 400 on a line the sender could not correct, and
	// litres that could never be reported (audit §33).
	if _, known := fuelLabels[draft.FuelType]; draft.FuelType != "" && !known {
		nexus.Error(w, http.StatusBadRequest, "энэ түлшний төрлийг мэдэхгүй байна")
		return
	}
	if draft.FuelType == "" {
		nexus.Error(w, http.StatusBadRequest, "түлшний төрөл заавал шаардлагатай")
		return
	}
	stationID := chi.URLParam(r, "id")
	if !isUUID(stationID) {
		nexus.Error(w, http.StatusBadRequest, "id буруу хэлбэртэй байна")
		return
	}

	// Negative values are refused here, not left to the CHECK (migration 00015):
	// a capacity below zero used to mean "no ceiling", and a violated constraint
	// would come back as a generic 500.
	if (draft.PriceMNT != nil && *draft.PriceMNT < 0) ||
		(draft.CapacityLiters != nil && *draft.CapacityLiters < 0) {
		nexus.Error(w, http.StatusBadRequest, "үнэ ба багтаамж сөрөг байж болохгүй")
		return
	}

	// The station has to be one this organisation owns. The tenant is named in
	// the query rather than left to the policy: an oversight body can READ every
	// station (`oversight_read`), so a bare "is it visible" check let a regulator
	// write grades — prices on the public map — onto a company's forecourt.
	var exists bool
	if err := m.db.QueryRow(r.Context(),
		`SELECT EXISTS (SELECT 1 FROM petro_stations WHERE id = $1 AND tenant_id = $2::uuid)`,
		stationID, tenantID).Scan(&exists); err != nil || !exists {
		nexus.Error(w, http.StatusNotFound, "ШТС олдсонгүй")
		return
	}

	var grade StationGrade
	err = m.db.QueryRow(r.Context(), `
		INSERT INTO petro_station_inventory
		       (station_id, tenant_id, fuel_type, fuel_label, price_mnt,
		        tank_capacity_liters, current_stock_liters, status, last_reported_at)
		VALUES ($1, $2, $3, COALESCE(NULLIF($4, ''), $3), COALESCE($5, 0),
		        COALESCE($6, 0), 0, COALESCE($7, 'available'), NOW())
		ON CONFLICT (station_id, fuel_type) DO UPDATE SET
		        fuel_label           = COALESCE(NULLIF($4, ''), petro_station_inventory.fuel_label),
		        price_mnt            = COALESCE($5, petro_station_inventory.price_mnt),
		        tank_capacity_liters = COALESCE($6, petro_station_inventory.tank_capacity_liters),
		        status               = COALESCE($7, petro_station_inventory.status),
		        last_reported_at     = CASE WHEN $7 IS NULL OR $7 = petro_station_inventory.status
		                                    THEN petro_station_inventory.last_reported_at
		                                    ELSE NOW() END
		RETURNING fuel_type, fuel_label, price_mnt::float8,
		          tank_capacity_liters::float8, current_stock_liters::float8,
		          status, last_reported_at`,
		stationID, tenantID, draft.FuelType, draft.FuelLabel, draft.PriceMNT,
		draft.CapacityLiters, draft.Status).
		Scan(&grade.FuelType, &grade.FuelLabel, &grade.PriceMNT, &grade.CapacityLiters,
			&grade.CurrentLiters, &grade.Status, &grade.ReportedAt)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "түлшний мэдээллийг хадгалж чадсангүй")
		return
	}

	nexus.Audit(r.Context(), tenantID, claims.UserID, "petro.station.grade_set", stationID,
		map[string]any{"fuel_type": grade.FuelType, "price_mnt": grade.PriceMNT, "status": grade.Status})

	nexus.JSON(w, http.StatusOK, grade)
}

// handleDeleteStationGrade stops a forecourt selling a grade.
//
// The row goes rather than being marked unavailable: "we do not sell diesel"
// and "we are out of diesel" are different sentences, and `status` is how the
// second one is said.
func (m *Module) handleDeleteStationGrade(w http.ResponseWriter, r *http.Request) {
	claims, err := nexus.UserFromContext(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	tenantID, ok := nexus.RequireWorkspace(w, r)
	if !ok {
		return
	}
	stationID, fuelType := chi.URLParam(r, "id"), chi.URLParam(r, "fuelType")
	if !isUUID(stationID) {
		nexus.Error(w, http.StatusBadRequest, "id буруу хэлбэртэй байна")
		return
	}

	tag, err := m.db.Exec(r.Context(),
		`DELETE FROM petro_station_inventory WHERE station_id = $1 AND fuel_type = $2`,
		stationID, fuelType)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "устгаж чадсангүй")
		return
	}
	if tag.RowsAffected() == 0 {
		nexus.Error(w, http.StatusNotFound, "энэ ШТС-д тийм түлш бүртгэгдээгүй байна")
		return
	}

	nexus.Audit(r.Context(), tenantID, claims.UserID, "petro.station.grade_removed", stationID,
		map[string]any{"fuel_type": fuelType})

	w.WriteHeader(http.StatusNoContent)
}
