/*
 * Gerege Nexus
 * Copyright (c) 2026 Gerege Systems Development Team, Gerege Nomadica Foundation
 * Distributed under the Apache 2.0 License.
 *
 * Package fuel is the fuel distribution network (io.gerege.nexus.petro): the
 * stations a citizen can reach, the daily entitlement they draw against, and
 * the voucher that turns one into the other.
 *
 * # What this is, at this commit
 *
 * The station register and the two views of it: the operator's, gated and
 * scoped to one organisation by row-level policy, and the citizen's, reached
 * without a session and answering across every operator at once. Neither the
 * entitlement nor the voucher exists yet — see BENZIN_NEXUS_PORT_PLAN.md §Ү3
 * and §Ү4.
 *
 * The domain it will carry is modelled already, in another repository and on
 * another platform: benzin-gerege-mn/backend/pkg/benzin. That code is not
 * copied here. Its data model is sound and its mechanism is not — it rations
 * by litres against a vehicle plate and binds a voucher to one station, where
 * this one rations by tugrik against a citizen and is spendable at any pump.
 * Porting the mechanism would port the wrong rule.
 */
package petro

import (
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gerege-systems/open-gerege-nexus/backend/pkg/nexus"
	"github.com/go-chi/chi/v5"
)

// ID is the catalogue identifier.
const ID = "io.gerege.nexus.petro"

//go:embed migrations/*.sql
var migrations embed.FS

// Module is the fuel network's compile-time app module.
type Module struct {
	db    nexus.DB
	perms nexus.PermissionStore
}

// New builds the module and registers it in the compile-time app registry.
//
// One argument, as the SDK asks: the platform lends its modules more over time,
// and a constructor that grew a parameter for each would be a signature every
// distribution has to chase.
//
// The schema travels with the module rather than living in db/migrations. That
// is what lets this app leave for a distribution repository later without
// leaving its tables behind in every deployment that never installed it — the
// state 28 tables were in before migration 00075.
func New(p nexus.Platform) *Module {
	m := &Module{db: p.DB(), perms: p.Permissions()}
	nexus.Register(m)

	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		// Unreachable: the path is a compile-time constant checked by //go:embed.
		panic("fuel: embedded migrations: " + err.Error())
	}
	nexus.Migrations(ID, sub)

	// The chain on /metrics. National aggregates only, and no tenant label —
	// see metrics.go for why a per-company series would be a competitor's read
	// of a business kept for sixty days.
	RegisterMetrics(m.db)

	// The seven reports the requirement names, on the platform's engine —
	// Excel, CSV, schedules and e-mail delivery come with it. See reports.go.
	RegisterReports()

	// The reporting periods and the national day table are started by the
	// platform through Start (jobs.go), not here: the constructor has no
	// context that shutdown cancels.

	return m
}

func (m *Module) ID() string      { return ID }
func (m *Module) Name() string    { return "Fuel Network" }
func (m *Module) Version() string { return "0.1.0" }

func (m *Module) Dependencies() []nexus.Dependency { return nil }

// MenuPermission is what a member needs for this app's entries to appear.
func (m *Module) MenuPermission() string { return "petro.read" }

// RoutePermissionPrefix is empty, and that is an answer rather than an omission.
//
// The platform's prefix gate derives the permission from the HTTP method:
// `fuel.read` for a GET, `fuel.manage` for anything that writes. That is the
// right rule for the operator's half of this module and the wrong rule for the
// other half, because this module serves two different kinds of person:
//
//	an operator   manages a company's stations and stock — read and manage
//	a citizen     draws their own share of a national ration — a POST, and one
//	              that must not require the permission to edit somebody's
//	              forecourt
//
// Under the prefix gate, taking a 20,000₮ voucher would have needed
// `fuel.manage`. So the module gates its own routes: RequirePermission on the
// operator ones, a session and nothing more on the citizen ones. The
// installation check still applies to every route here — that part is the
// middleware's and is not being declined.
func (m *Module) RoutePermissionPrefix() string { return "" }

// Permissions names who may read the network and who may change it.
//
// DefaultRoles rather than the suffix rule: a station's stock is this
// organisation's own operational data, so every member reads it, and only a
// manager edits. Stating it here means the installer does not have to guess
// from the end of the code.
func (m *Module) Permissions() []nexus.PermissionDefinition {
	return []nexus.PermissionDefinition{
		{
			Code:         "petro.read",
			Name:         "Read the fuel network",
			Description:  "View stations, stock levels and queues",
			DefaultRoles: []string{nexus.DefaultRoleManager, nexus.DefaultRoleUser},
		},
		{
			Code:         "petro.manage",
			Name:         "Manage the fuel network",
			Description:  "Register stations, adjust stock and configure vouchers",
			DefaultRoles: []string{nexus.DefaultRoleManager},
		},
		{
			// Sending the state a figure is a clerk's job, not a manager's, and
			// a company where only the manager can report is a company that
			// reports late. Separate from petro.manage for the same reason:
			// submitting must not require the right to edit the register.
			Code:         "petro.report.submit",
			Name:         "Submit regulatory reports",
			Description:  "Send stock, sales and price figures to the regulator",
			DefaultRoles: []string{nexus.DefaultRoleManager, nexus.DefaultRoleUser},
		},
		{
			// Held inside a supervisory organisation. It is the second of two
			// locks: petro_oversight_bodies decides which organisations may see
			// across companies at all, and this decides who inside one of them
			// may act. Neither alone is enough.
			Code:         "petro.oversight",
			Name:         "Supervise the national fuel network",
			Description:  "Review submissions, act on sites and read the national picture",
			DefaultRoles: []string{nexus.DefaultRoleManager},
		},
	}
}

// Menus is the one screen that exists.
//
// Every supported locale is filled in: a missing one does not fail, it falls
// back to English, which is how a screen ends up showing three languages at
// once. internal/apps.TestEveryMenuLabelCoversEverySupportedLocale asserts it.
func (m *Module) Menus() []nexus.MenuDefinition {
	return []nexus.MenuDefinition{
		{
			ID:    "petro_stations",
			Label: "Fuel network",
			Path:  "/petro",
			Icon:  "fuel",
			Order: 10,
			Labels: map[string]string{
				"mn": "Шатахууны сүлжээ",
				"ar": "شبكة الوقود",
				"zh": "燃料网络",
				"fr": "Réseau de carburant",
				"ru": "Топливная сеть",
				"es": "Red de combustible",
			},
		},
		{
			ID:    "petro_report",
			Label: "Regulatory report",
			Path:  "/petro/report",
			Icon:  "file-check",
			Order: 11,
			Labels: map[string]string{
				"mn": "Тайлан илгээх",
				"ar": "تقرير تنظيمي",
				"zh": "监管报告",
				"fr": "Rapport réglementaire",
				"ru": "Отчёт регулятору",
				"es": "Informe regulatorio",
			},
		},
		{
			ID:    "petro_oversight",
			Label: "National oversight",
			Path:  "/petro/oversight",
			Icon:  "shield-check",
			Order: 12,
			// Every member reads the network, only a supervisory body's
			// officials act on it: without this the entry sat in everyone's
			// sidebar and opened onto a 403.
			Permission: "petro.oversight",
			Labels: map[string]string{
				"mn": "Улсын хяналт",
				"ar": "الرقابة الوطنية",
				"zh": "国家监管",
				"fr": "Supervision nationale",
				"ru": "Государственный надзор",
				"es": "Supervisión nacional",
			},
		},
	}
}

// RegisterRoutes mounts the module's API under its own name.
//
// The router handed over is the root one, so mounting outside the gate is one
// line and looks exactly like mounting inside it. Both kinds are here, and
// which is which is the most consequential thing in this file.
func (m *Module) RegisterRoutes(r chi.Router, tenantAuthMiddleware func(http.Handler) http.Handler) {
	// The deployment console lives outside workspace authentication. Its
	// handler validates the console's own session before reading across fuel
	// operators; see overview.go.
	r.Get("/api/platform/v1/petro/overview", m.handleOperatorOverview)

	// Appointing a supervisory body is a deployment act. Inside the tenant
	// gate an organisation could appoint itself, which is the whole of the
	// access model undone by one POST.
	r.Get("/api/platform/v1/petro/oversight-bodies", m.handleOversightBodies)
	r.Post("/api/platform/v1/petro/oversight-bodies", m.handleOversightBodies)

	r.Route("/api/v1/petro", func(fr chi.Router) {
		fr.Use(tenantAuthMiddleware)

		// The operator's half. Each route names the permission it needs.
		//
		// RoutePermissionPrefix is empty — see the note on it — so the platform
		// gates nothing here beyond checking the app is installed. That makes
		// these calls the whole of the check, and a route added below without
		// one is a route any member of the organisation may make.
		fr.With(nexus.RequirePermission(m.perms, "petro.read")).
			Get("/stations", m.handleListStations)

		// Keeping the register rather than only reading it — see station.go.
		// Adding a forecourt, correcting one, retiring one, and saying which
		// grades it sells at what price. No route here sets a litre figure:
		// stock rises through /trips/{id}/receive and nowhere else.
		fr.With(nexus.RequirePermission(m.perms, "petro.manage")).
			Post("/stations", m.handleCreateStation)
		fr.With(nexus.RequirePermission(m.perms, "petro.manage")).
			Patch("/stations/{id}", m.handleUpdateStation)
		fr.With(nexus.RequirePermission(m.perms, "petro.manage")).
			Delete("/stations/{id}", m.handleDeleteStation)
		fr.With(nexus.RequirePermission(m.perms, "petro.manage")).
			Put("/stations/{id}/grades", m.handleSetStationGrade)
		fr.With(nexus.RequirePermission(m.perms, "petro.manage")).
			Delete("/stations/{id}/grades/{fuelType}", m.handleDeleteStationGrade)
		// A tracker reporting where a tanker is. Gated, and narrowed to the
		// caller's own organisation by the row-level policy — an operator must
		// not be able to move somebody else's lorry.
		fr.With(nexus.RequirePermission(m.perms, "petro.manage")).
			Post("/trips/{id}/telemetry", m.handleTripTelemetry)

		// Unloading a tanker. The only place a forecourt's stock goes up, and
		// the last link in the chain a batch travels — see receipt.go.
		fr.With(nexus.RequirePermission(m.perms, "petro.manage")).
			Post("/trips/{id}/receive", m.handleReceiveDelivery)
		fr.With(nexus.RequirePermission(m.perms, "petro.read")).
			Get("/stations/{id}/receipts", m.handleListReceipts)

		// The top of the chain: what crossed the border, and the bases it was
		// unloaded into. See border.go and depot.go.
		//
		// Everything that changes a customs record or a tank level asks for
		// fuel.manage. An ordinary member reads the register — which grades the
		// company holds, and where — and a manager is the one who can say a
		// consignment cleared or that sixty thousand litres went into tank 3.
		fr.With(nexus.RequirePermission(m.perms, "petro.read")).
			Get("/shipments", m.handleListShipments)
		fr.With(nexus.RequirePermission(m.perms, "petro.manage")).
			Post("/shipments", m.handleCreateShipment)
		fr.With(nexus.RequirePermission(m.perms, "petro.manage")).
			Post("/shipments/{id}/status", m.handleAdvanceShipment)

		fr.With(nexus.RequirePermission(m.perms, "petro.read")).
			Get("/depots", m.handleListDepots)
		fr.With(nexus.RequirePermission(m.perms, "petro.manage")).
			Post("/depots", m.handleCreateDepot)
		fr.With(nexus.RequirePermission(m.perms, "petro.manage")).
			Delete("/depots/{id}", m.handleDeleteDepot)
		fr.With(nexus.RequirePermission(m.perms, "petro.manage")).
			Post("/depots/{id}/tanks", m.handleCreateTank)
		fr.With(nexus.RequirePermission(m.perms, "petro.manage")).
			Patch("/depots/{id}/tanks/{tankId}", m.handleUpdateTank)
		fr.With(nexus.RequirePermission(m.perms, "petro.read")).
			Get("/depots/{id}/receipts", m.handleListDepotReceipts)
		fr.With(nexus.RequirePermission(m.perms, "petro.manage")).
			Post("/depots/{id}/receipts", m.handleReceiveIntoDepot)

		// The two acts that take fuel OUT — the chain's missing minus sign.
		// Loading a lorry draws down a base's tank; dispensing draws down a
		// forecourt's and closes the voucher, if one was presented. Before
		// these existed every litre that left a depot was still counted there
		// and counted again where it arrived (audit §1, §2).
		fr.With(nexus.RequirePermission(m.perms, "petro.manage")).
			Post("/depots/{id}/dispatch", m.handleDispatchFromDepot)
		fr.With(nexus.RequirePermission(m.perms, "petro.manage")).
			Post("/stations/{id}/sales", m.handleRecordSale)

		// A citizen's own ration and the vouchers drawn on it.
		//
		// Inside the gate: taking a share of a national ration is not something
		// a stranger does. The identity is the session's — see entitlement.go
		// for why there is nowhere in the request to put one.
		// The regulatory loop's company half: the periods it must answer, the
		// form or the workbook it answers with, and its own history.
		//
		// Reading is petro.read and sending is petro.report.submit. The two are
		// split because the person who sends the daily figures in a fuel
		// company is not the person who may add a forecourt.
		fr.With(nexus.RequirePermission(m.perms, "petro.read")).
			Get("/report/periods", m.handleListPeriods)
		fr.With(nexus.RequirePermission(m.perms, "petro.read")).
			Get("/report/periods/{id}/prefill", m.handlePrefill)
		fr.With(nexus.RequirePermission(m.perms, "petro.read")).
			Get("/report/periods/{id}/template.xlsx", m.handleTemplate)
		fr.With(nexus.RequirePermission(m.perms, "petro.report.submit")).
			Post("/report/periods/{id}/submissions", m.handleSubmit)
		fr.With(nexus.RequirePermission(m.perms, "petro.report.submit")).
			Post("/report/periods/{id}/submissions/excel", m.handleSubmitExcel)
		fr.With(nexus.RequirePermission(m.perms, "petro.read")).
			Get("/report/submissions", m.handleListSubmissions)
		fr.With(nexus.RequirePermission(m.perms, "petro.read")).
			Get("/report/submissions/{id}", m.handleReadSubmission)
		fr.With(nexus.RequirePermission(m.perms, "petro.read")).
			Get("/policy", m.handleReadPolicy)

		// Movements: opened by the sender, closed by the receiver.
		fr.With(nexus.RequirePermission(m.perms, "petro.read")).
			Get("/movements", m.handleListMovements)
		fr.With(nexus.RequirePermission(m.perms, "petro.manage")).
			Post("/movements", m.handleOpenMovement)
		fr.With(nexus.RequirePermission(m.perms, "petro.manage")).
			Post("/movements/{id}/receive", m.handleCloseMovement)

		// The register's own additions.
		fr.With(nexus.RequirePermission(m.perms, "petro.manage")).
			Patch("/registry/{kind}/{id}/code", m.handleSetNationalCode)
		fr.With(nexus.RequirePermission(m.perms, "petro.manage")).
			Patch("/stations/{id}/census", m.handleCensus)
		fr.With(nexus.RequirePermission(m.perms, "petro.read")).
			Get("/census/summary", m.handleCensusSummary)

		// The state's half. Every one of these also asks petro_is_oversight()
		// inside the handler: the permission says who in the ministry may act,
		// the table says which organisation is the ministry.
		fr.With(nexus.RequirePermission(m.perms, "petro.oversight")).
			Get("/oversight/queue", m.handleReviewQueue)
		fr.With(nexus.RequirePermission(m.perms, "petro.oversight")).
			Post("/oversight/submissions/{id}/approve", m.handleReview(StatusApproved))
		fr.With(nexus.RequirePermission(m.perms, "petro.oversight")).
			Post("/oversight/submissions/{id}/return", m.handleReview(StatusReturned))
		fr.With(nexus.RequirePermission(m.perms, "petro.oversight")).
			Get("/oversight/dashboard", m.handleNationalDashboard)
		fr.With(nexus.RequirePermission(m.perms, "petro.oversight")).
			Get("/oversight/gaps", m.handleGaps)
		fr.With(nexus.RequirePermission(m.perms, "petro.oversight")).
			Get("/oversight/reconciliation", m.handleReconciliation)
		fr.With(nexus.RequirePermission(m.perms, "petro.oversight")).
			Post("/oversight/daily/refresh", m.handleRefreshDaily)
		fr.With(nexus.RequirePermission(m.perms, "petro.oversight")).
			Post("/oversight/sites/{kind}/{id}/status", m.handleSetSiteStatus)
		fr.With(nexus.RequirePermission(m.perms, "petro.oversight")).
			Post("/oversight/movements/{id}/dispute", m.handleDisputeMovement)

		fr.Get("/me/entitlement", m.handleMyEntitlement)
		fr.Get("/me/vouchers", m.handleMyVouchers)
		fr.Post("/me/vouchers", m.handleIssueVoucher)
	})

	// The citizen's surface. Outside the gate on purpose: a person looking for
	// somewhere to buy fuel has no organisation and, at this point, no session.
	//
	// It is named in pkg/platform/route_policy_test.go with this reasoning, and
	// that naming is the point — the test walks the real routing table and
	// fails on anything answering 200 without a session that is not on the
	// list, so a private route cannot become public by where a line was put.
	//
	// Rate limited because nothing else limits it. Every other public route on
	// this platform either costs an attacker a credential or is a health check;
	// this one is a database query anybody can ask for, so the budget is
	// stated here rather than left to nginx, which does not know what this is.
	r.Route("/api/v1/petro/public", func(pr chi.Router) {
		pr.Use(nexus.RateLimit(60, 20))
		pr.Get("/stations", m.handlePublicStations)
		// Open data: national totals by grade, with the share of the country
		// they were computed from. Kenya's 2026 standard — published on the
		// same schedule every day, weekends included.
		pr.Get("/daily", m.handlePublicDaily)
	})
}

// Station is one filling station as its operator sees it.
//
// The operational columns are here — pump count, queue, stock — because this is
// the tenant's own view. The citizen's is a different, narrower shape served
// from a different route; the two are not one struct with fields blanked out,
// because a field blanked out is a field somebody will forget to blank.
type Station struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Brand         string  `json:"brand"`
	BrandLabel    string  `json:"brand_label"`
	Lat           float64 `json:"lat"`
	Lon           float64 `json:"lon"`
	Aimag         string  `json:"aimag"`
	District      string  `json:"district"`
	Address       string  `json:"address"`
	Phone         string  `json:"phone"`
	OpeningHours  string  `json:"opening_hours"`
	TotalPumps    int     `json:"total_pumps"`
	ActivePumps   int     `json:"active_pumps"`
	QueueCount    int     `json:"current_queue_count"`
	Status        string  `json:"status"`
	VoucherOpen   bool    `json:"is_voucher_enabled"`
	FuelTypeCount int     `json:"fuel_type_count"`
	// Fuels are the grades this forecourt sells. They travel with the station
	// for the reason the depot's tanks travel with the depot: a station without
	// its grades is a name and a dot on a map, and every screen that asks for
	// one asks for the other in the next breath.
	Fuels []StationGrade `json:"fuels,omitempty"`
}

// StationGrade is one fuel grade at one forecourt.
//
// CurrentLiters is here to be read and is not settable: see station.go. It is
// the sum of what deliveries have put in the tank, and ReportedAt is when
// somebody last said anything about whether the grade could be had.
type StationGrade struct {
	FuelType       string     `json:"fuel_type"`
	FuelLabel      string     `json:"fuel_label"`
	PriceMNT       float64    `json:"price_mnt"`
	CapacityLiters float64    `json:"tank_capacity_liters"`
	CurrentLiters  float64    `json:"current_stock_liters"`
	Status         string     `json:"status"`
	ReportedAt     *time.Time `json:"last_reported_at"`
}

// handleListStations answers the stations this organisation operates.
//
// No tenant_id in the WHERE clause, and that is deliberate rather than
// forgotten: the row-level policy installed by this module's migration is what
// scopes it, and the pool binds every connection to the caller's organisation
// before the statement runs. The clause would be a second answer to a question
// the database already answers, and two answers drift.
//
// Bounded at a thousand. An operator with more stations than that needs paging
// rather than a bigger number, and saying so in a header beats a truncated list
// that looks complete.
func (m *Module) handleListStations(w http.ResponseWriter, r *http.Request) {
	if _, ok := nexus.RequireWorkspace(w, r); !ok {
		return
	}

	rows, err := m.db.Query(r.Context(), `
		SELECT s.id::text, s.name, s.brand, s.brand_label,
		       s.lat, s.lon, s.aimag, s.district, s.address, s.phone,
		       s.opening_hours, s.total_pumps, s.active_pumps,
		       s.current_queue_count, s.status, s.is_voucher_enabled,
		       COUNT(i.fuel_type)::int,
		       COALESCE(
		           json_agg(
		               json_build_object(
		                   'fuel_type', i.fuel_type,
		                   'fuel_label', i.fuel_label,
		                   'price_mnt', i.price_mnt::float8,
		                   'tank_capacity_liters', i.tank_capacity_liters::float8,
		                   'current_stock_liters', i.current_stock_liters::float8,
		                   'status', i.status,
		                   'last_reported_at', i.last_reported_at)
		               ORDER BY i.fuel_type)
		           FILTER (WHERE i.fuel_type IS NOT NULL),
		           '[]'::json)
		  FROM petro_stations s
		  LEFT JOIN petro_station_inventory i ON i.station_id = s.id
		 GROUP BY s.id
		 ORDER BY s.aimag, s.district, s.name
		 LIMIT 1000`)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not load the stations")
		return
	}
	defer rows.Close()

	stations := []Station{}
	for rows.Next() {
		var s Station
		if err := rows.Scan(&s.ID, &s.Name, &s.Brand, &s.BrandLabel,
			&s.Lat, &s.Lon, &s.Aimag, &s.District, &s.Address, &s.Phone,
			&s.OpeningHours, &s.TotalPumps, &s.ActivePumps,
			&s.QueueCount, &s.Status, &s.VoucherOpen, &s.FuelTypeCount,
			&s.Fuels); err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not read the stations")
			return
		}
		stations = append(stations, s)
	}
	if rows.Err() != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the stations")
		return
	}

	// An empty array rather than null: a client that maps over the response
	// should not have to ask which of the two it got.
	nexus.JSON(w, http.StatusOK, map[string]any{
		"stations": stations,
		"count":    len(stations),
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// The citizen's view
// ─────────────────────────────────────────────────────────────────────────────

// PublicStation is a station as somebody looking for fuel sees it.
//
// A separate type from Station, not the same one with fields left empty. The
// difference matters: a blanked field is a field somebody adds a value back
// into six months later without noticing which response it travels in, and the
// values withheld here — tank capacity, litres in the ground, the supply
// schedule behind them — are a competitor's read of an operator's business.
//
// What is here is what a person needs to choose where to drive: where it is,
// whose it is, whether it is open, what it sells and for how much.
type PublicStation struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Brand        string       `json:"brand"`
	BrandLabel   string       `json:"brand_label"`
	Lat          float64      `json:"lat"`
	Lon          float64      `json:"lon"`
	Aimag        string       `json:"aimag"`
	District     string       `json:"district"`
	Address      string       `json:"address"`
	OpeningHours string       `json:"opening_hours"`
	Status       string       `json:"status"`
	VoucherOpen  bool         `json:"is_voucher_enabled"`
	Fuels        []PublicFuel `json:"fuels"`

	// StockPercent is the fullest tank on the forecourt, or nil when no tank
	// here has a reported size.
	//
	// The fullest rather than the average: the question a pin has to answer at
	// a glance is "can I get fuel here", and a station with an empty 92 tank and
	// a full diesel one is not empty. Which fuel it is belongs to the popup,
	// where somebody is choosing rather than scanning.
	StockPercent *float64 `json:"stock_percent"`
}

// PublicFuel is one fuel a station sells, at the price it sells it for.
//
// A percentage of tank, never litres. The distinction is the whole reason this
// can be published: "this station is nearly out" is what a driver needs and
// cannot act on any other way, while "this station holds 18,400 litres" is a
// rival's read of a business — volumes, turnover, delivery schedule. A ratio
// carries the first and not the second.
//
// StockPercent is nil where nobody has reported a tank size. Nil rather than
// zero, because an unknown level and an empty one send a driver to opposite
// places.
type PublicFuel struct {
	Type         string   `json:"type"`
	Label        string   `json:"label"`
	Price        float64  `json:"price_mnt"`
	Status       string   `json:"status"`
	StockPercent *float64 `json:"stock_percent"`
}

// The most stations one call will answer with.
//
// The screen this feeds is a map, and a map shows a viewport. benzin's own
// client asked for five thousand on every app open — 500 stations with their
// fuels, personalised by the caller's coordinates so no cache could hold it,
// which at fifty thousand users a day is a hundred gigabytes of egress for a
// list that changes hourly. A bounded query is not a limitation here, it is
// the shape of the question.
const publicStationLimit = 300

// handlePublicStations answers the stations inside a map viewport.
//
// No tenant, by design: dbguard leaves a request with no organisation on the
// login role, outside the row-level policies, so this reads every operator's
// stations — which is the point, since a driver does not care whose brand is
// nearest. The filtering that keeps an operator's private columns out of the
// answer is in the SELECT list above, written by hand, because on this path the
// database is not going to do it.
//
// A bounding box is required. Without one the only honest answers are "all of
// them", which is the query this exists to replace, or a silent default
// somewhere near Ulaanbaatar, which is wrong for most of the country.
func (m *Module) handlePublicStations(w http.ResponseWriter, r *http.Request) {
	box, err := parseBBox(r.URL.Query().Get("bbox"))
	if err != nil {
		nexus.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	rows, err := m.db.Query(r.Context(), `
		SELECT s.id::text, s.name, s.brand, s.brand_label,
		       s.lat, s.lon, s.aimag, s.district, s.address,
		       s.opening_hours, s.status, s.is_voucher_enabled,
		       COALESCE(
		           json_agg(
		               json_build_object(
		                   'type', i.fuel_type,
		                   'label', i.fuel_label,
		                   'price_mnt', i.price_mnt,
		                   'status', i.status,
		                   'stock_percent', CASE
		                       WHEN i.tank_capacity_liters > 0
		                       THEN ROUND(
		                           LEAST(i.current_stock_liters / i.tank_capacity_liters, 1) * 100)
		                       END)
		               ORDER BY i.fuel_type)
		           FILTER (WHERE i.fuel_type IS NOT NULL),
		           '[]'::json),
		       MAX(CASE
		               WHEN i.tank_capacity_liters > 0
		               THEN ROUND(
		                   LEAST(i.current_stock_liters / i.tank_capacity_liters, 1) * 100)
		               END)
		  FROM petro_stations s
		  LEFT JOIN petro_station_inventory i ON i.station_id = s.id
		 WHERE s.lon BETWEEN $1 AND $3
		   AND s.lat BETWEEN $2 AND $4
		 GROUP BY s.id
		 ORDER BY s.name
		 LIMIT $5`,
		box.minLon, box.minLat, box.maxLon, box.maxLat, publicStationLimit)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not load the stations")
		return
	}
	defer rows.Close()

	stations := []PublicStation{}
	for rows.Next() {
		var s PublicStation
		var fuels []byte
		if err := rows.Scan(&s.ID, &s.Name, &s.Brand, &s.BrandLabel,
			&s.Lat, &s.Lon, &s.Aimag, &s.District, &s.Address,
			&s.OpeningHours, &s.Status, &s.VoucherOpen, &fuels, &s.StockPercent); err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not read the stations")
			return
		}
		if err := json.Unmarshal(fuels, &s.Fuels); err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not read the stations")
			return
		}
		stations = append(stations, s)
	}
	if rows.Err() != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the stations")
		return
	}

	// Says so when the viewport held more than one answer carries, rather than
	// letting a truncated list read as a complete one. A client that sees this
	// zooms in; a client that ignores it is at least not misled by silence.
	nexus.JSON(w, http.StatusOK, map[string]any{
		"stations":  stations,
		"count":     len(stations),
		"truncated": len(stations) == publicStationLimit,
	})
}

// bbox is a map viewport in degrees.
type bbox struct{ minLon, minLat, maxLon, maxLat float64 }

// parseBBox reads "minLon,minLat,maxLon,maxLat".
//
// The order is the one GeoJSON and every mapping client use, so a caller
// copying a viewport out of MapLibre does not have to reorder it.
//
// The area is capped. An unbounded box is the "all of them" query wearing a
// parameter, and a caller that wants the whole country wants a different
// endpoint than the one feeding a map — one that can be cached, because it
// would be the same answer for everybody.
func parseBBox(raw string) (bbox, error) {
	if raw == "" {
		return bbox{}, errors.New("bbox is required, as minLon,minLat,maxLon,maxLat")
	}
	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return bbox{}, errors.New("bbox takes four numbers: minLon,minLat,maxLon,maxLat")
	}
	var values [4]float64
	for i, part := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return bbox{}, errors.New("bbox takes four numbers: minLon,minLat,maxLon,maxLat")
		}
		values[i] = v
	}
	box := bbox{values[0], values[1], values[2], values[3]}
	if box.minLon >= box.maxLon || box.minLat >= box.maxLat {
		return bbox{}, errors.New("bbox is empty: min must be less than max")
	}
	// Mongolia spans about 33 degrees of longitude and 11 of latitude. A box
	// larger than the country is not a viewport.
	if box.maxLon-box.minLon > 35 || box.maxLat-box.minLat > 15 {
		return bbox{}, errors.New("bbox is too large; zoom in")
	}
	return box, nil
}
