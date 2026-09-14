/*
 * Gerege Nexus
 * Copyright (c) 2026 Gerege Systems Development Team, Gerege Nomadica Foundation
 * Distributed under the Apache 2.0 License.
 *
 * The depot: tanks, and the two events that move their level.
 *
 * A depot tank is the one place in this system where fuel sits still for long
 * enough to be counted twice — once when a consignment goes in, once when a
 * tanker draws out. Both are recorded here, and both are transactions, because
 * a level that can be written without a matching document is a level anybody
 * can explain away.
 *
 * # Why the tank cannot simply be set
 *
 * There is no endpoint that assigns `current_liters`. Every change is a receipt
 * in or a load out, and the level follows from them. The gauge reading is a
 * separate field precisely so that a discrepancy between what the paperwork
 * implies and what the dipstick says survives as a discrepancy rather than
 * being resolved by whoever typed last.
 *
 * # Overfilling is the database's refusal, not this file's
 *
 * `tank_within_capacity` is a CHECK constraint. A handler that looked first and
 * inserted second would let two simultaneous deliveries each see room for one.
 * Here the write is attempted and Postgres refuses it — which is also true of
 * the demo dispatcher, of a future import job, and of anything else that has
 * not been written yet.
 */

package petro

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gerege-systems/open-gerege-nexus/backend/pkg/nexus"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// Depot is a storage base as its operator sees it.
type Depot struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Brand           string   `json:"brand"`
	Aimag           string   `json:"aimag"`
	District        string   `json:"district"`
	Address         string   `json:"address"`
	Lat             *float64 `json:"lat"`
	Lon             *float64 `json:"lon"`
	HasRailSiding   bool     `json:"has_rail_siding"`
	RailStationCode string   `json:"rail_station_code"`
	Status          string   `json:"status"`
	TankCount       int      `json:"tank_count"`
	CapacityLiters  float64  `json:"capacity_liters"`
	CurrentLiters   float64  `json:"current_liters"`
	// FillPercent is the whole base, tanks summed. Nil when no tank is
	// registered — an unknown base and an empty one are different facts.
	FillPercent *float64 `json:"fill_percent"`
	Tanks       []Tank   `json:"tanks,omitempty"`
}

// Tank is one vessel at a depot.
type Tank struct {
	ID              string   `json:"id"`
	DepotID         string   `json:"depot_id"`
	TankNo          string   `json:"tank_no"`
	TankType        string   `json:"tank_type"`
	FuelType        string   `json:"fuel_type"`
	FuelLabel       string   `json:"fuel_label"`
	CapacityLiters  float64  `json:"capacity_liters"`
	CurrentLiters   float64  `json:"current_liters"`
	FillPercent     float64  `json:"fill_percent"`
	TemperatureC    *float64 `json:"temperature_c"`
	DensityKgM3     *float64 `json:"density_kg_m3"`
	SafetyStatus    string   `json:"safety_status"`
	NextInspectedAt *string  `json:"next_inspection_at"`
}

// handleListDepots is this organisation's bases, with their tanks.
//
// The tanks come with them rather than behind a second call: a depot without
// its levels is a name and a dot on a map, and every screen that asks for one
// asks for the other in the next breath.
func (m *Module) handleListDepots(w http.ResponseWriter, r *http.Request) {
	if _, ok := nexus.RequireWorkspace(w, r); !ok {
		return
	}

	rows, err := m.db.Query(r.Context(), `
		SELECT d.id::text, d.name, d.brand, d.aimag, d.district, d.address,
		       d.lat, d.lon, d.has_rail_siding, d.rail_station_code, d.status,
		       COUNT(t.id)::int,
		       COALESCE(SUM(t.capacity_liters), 0)::float8,
		       COALESCE(SUM(t.current_liters), 0)::float8,
		       COALESCE(
		           json_agg(
		               json_build_object(
		                   'id', t.id::text,
		                   'depot_id', d.id::text,
		                   'tank_no', t.tank_no,
		                   'tank_type', t.tank_type,
		                   'fuel_type', t.fuel_type,
		                   'fuel_label', t.fuel_label,
		                   'capacity_liters', t.capacity_liters::float8,
		                   'current_liters', t.current_liters::float8,
		                   'fill_percent', ROUND(t.current_liters / t.capacity_liters * 100, 1),
		                   'temperature_c', t.temperature_c::float8,
		                   'density_kg_m3', t.density_kg_m3::float8,
		                   'safety_status', t.safety_status,
		                   'next_inspection_at', t.next_inspection_at)
		               ORDER BY t.tank_no)
		           FILTER (WHERE t.id IS NOT NULL),
		           '[]'::json)
		  FROM petro_depots d
		  LEFT JOIN petro_depot_tanks t ON t.depot_id = d.id
		 GROUP BY d.id
		 ORDER BY d.aimag, d.name
		 LIMIT 500`)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not load the depots")
		return
	}
	defer rows.Close()

	depots := []Depot{}
	for rows.Next() {
		var d Depot
		var tanks []byte
		if err := rows.Scan(&d.ID, &d.Name, &d.Brand, &d.Aimag, &d.District, &d.Address,
			&d.Lat, &d.Lon, &d.HasRailSiding, &d.RailStationCode, &d.Status,
			&d.TankCount, &d.CapacityLiters, &d.CurrentLiters, &tanks); err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not read the depots")
			return
		}
		if err := json.Unmarshal(tanks, &d.Tanks); err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not read the depots")
			return
		}
		if d.CapacityLiters > 0 {
			percent := d.CurrentLiters / d.CapacityLiters * 100
			d.FillPercent = &percent
		}
		depots = append(depots, d)
	}
	if rows.Err() != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the depots")
		return
	}

	nexus.JSON(w, http.StatusOK, map[string]any{"depots": depots, "count": len(depots)})
}

// DepotDraft registers a base.
type DepotDraft struct {
	Name            string   `json:"name"`
	Brand           string   `json:"brand"`
	Aimag           string   `json:"aimag"`
	District        string   `json:"district"`
	Address         string   `json:"address"`
	Lat             *float64 `json:"lat"`
	Lon             *float64 `json:"lon"`
	HasRailSiding   bool     `json:"has_rail_siding"`
	RailStationCode string   `json:"rail_station_code"`
}

// handleCreateDepot registers a base.
func (m *Module) handleCreateDepot(w http.ResponseWriter, r *http.Request) {
	claims, err := nexus.UserFromContext(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	tenantID, ok := nexus.RequireWorkspace(w, r)
	if !ok {
		return
	}

	var draft DepotDraft
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&draft); err != nil {
		nexus.Error(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if draft.Name == "" {
		nexus.Error(w, http.StatusBadRequest, "баазын нэр заавал шаардлагатай")
		return
	}
	aimag, known := normalizeAimag(draft.Aimag)
	if !known {
		nexus.Error(w, http.StatusBadRequest, aimagMessage)
		return
	}
	draft.Aimag = aimag

	var depot Depot
	err = m.db.QueryRow(r.Context(), `
		INSERT INTO petro_depots
		       (tenant_id, name, brand, aimag, district, address, lat, lon,
		        has_rail_siding, rail_station_code)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id::text, name, brand, aimag, district, address, lat, lon,
		          has_rail_siding, rail_station_code, status`,
		tenantID, draft.Name, draft.Brand, draft.Aimag, draft.District, draft.Address,
		draft.Lat, draft.Lon, draft.HasRailSiding, draft.RailStationCode).
		Scan(&depot.ID, &depot.Name, &depot.Brand, &depot.Aimag, &depot.District,
			&depot.Address, &depot.Lat, &depot.Lon, &depot.HasRailSiding,
			&depot.RailStationCode, &depot.Status)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not register the depot")
		return
	}

	nexus.Audit(r.Context(), tenantID, claims.UserID, "petro.depot.created", depot.ID,
		map[string]any{"name": depot.Name})

	nexus.JSON(w, http.StatusCreated, depot)
}

// handleDeleteDepot removes a base that has not been used.
//
// ШТС-д устгал байсан ч баазад байгаагүй нь тэгш бус байдал байв: андуурч
// бүртгэсэн бааз — буруу нэр, давхардсан бичлэг — бүтээгдэхүүнээр дамжуулан
// хэзээ ч арилахгүй. Зохицуулагч түүнийг түдгэлзүүлж чадах ч эзэмшигч
// байгууллага өөрийн алдааг өөрөө засаж чадахгүй байлаа.
//
// Хамгаалалт нь ШТС-ынхтай ижил зарчим: ТҮҮХТЭЙ зүйлийг устгахгүй. Хүлээн
// авалт бүртгэгдсэн, эсвэл рейс гарсан бааз бол тайлангийн ул мөрийн нэг хэсэг
// бөгөөд түүнийг устгах нь өнгөрсөн тоог тайлбарлах боломжгүй болгоно. Түлш
// байгаа сав ч мөн адил: хоосон биш савтай баазыг устгах нь литрийг бүртгэлээс
// алга болгоно.
func (m *Module) handleDeleteDepot(w http.ResponseWriter, r *http.Request) {
	claims, err := nexus.UserFromContext(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	tenantID, ok := nexus.RequireWorkspace(w, r)
	if !ok {
		return
	}
	depotID := chi.URLParam(r, "id")
	if !isUUID(depotID) {
		nexus.Error(w, http.StatusBadRequest, "id буруу хэлбэртэй байна")
		return
	}

	var receipts, trips, fuelled int
	if err := m.db.QueryRow(r.Context(), `
		SELECT (SELECT COUNT(*)::int FROM petro_depot_receipts WHERE depot_id = $1),
		       (SELECT COUNT(*)::int FROM petro_dispatch_trips
		         WHERE from_depot_id = $1 AND status <> 'cancelled'),
		       (SELECT COUNT(*)::int FROM petro_depot_tanks
		         WHERE depot_id = $1 AND current_liters > 0)`,
		depotID).Scan(&receipts, &trips, &fuelled); err != nil {
		nexus.Error(w, http.StatusInternalServerError, "баазыг шалгаж чадсангүй")
		return
	}
	if receipts > 0 || trips > 0 {
		nexus.Error(w, http.StatusConflict,
			"энэ бааз хүлээн авалт эсвэл рейстэй байна — устгахын оронд төлөвийг нь хаалттай болго")
		return
	}
	if fuelled > 0 {
		nexus.Error(w, http.StatusConflict, "савнуудад түлш байна — эхлээд хоослоно уу")
		return
	}

	// Савууд нь баазтайгаа хамт явна: хоосон, түүхгүй сав нь баазаас гадуур
	// утгагүй бөгөөд түүнийг үлдээх нь эзэнгүй мөр үлдээнэ.
	tag, err := m.db.Exec(r.Context(), `DELETE FROM petro_depots WHERE id = $1`, depotID)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "баазыг устгаж чадсангүй")
		return
	}
	if tag.RowsAffected() == 0 {
		nexus.Error(w, http.StatusNotFound, "бааз олдсонгүй")
		return
	}

	nexus.Audit(r.Context(), tenantID, claims.UserID, "petro.depot.deleted", depotID, nil)

	w.WriteHeader(http.StatusNoContent)
}

// TankDraft registers a vessel.
//
// No opening level. A tank enters the system empty and fills through receipts,
// because an opening figure typed into a form is a quantity of fuel that
// entered the country through a text box.
type TankDraft struct {
	TankNo         string  `json:"tank_no"`
	TankType       string  `json:"tank_type"`
	FuelType       string  `json:"fuel_type"`
	CapacityLiters float64 `json:"capacity_liters"`
}

// handleCreateTank adds a vessel to a base.
func (m *Module) handleCreateTank(w http.ResponseWriter, r *http.Request) {
	claims, err := nexus.UserFromContext(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	tenantID, ok := nexus.RequireWorkspace(w, r)
	if !ok {
		return
	}
	depotID := chi.URLParam(r, "id")
	if !isUUID(depotID) {
		nexus.Error(w, http.StatusBadRequest, "id буруу хэлбэртэй байна")
		return
	}

	var draft TankDraft
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&draft); err != nil {
		nexus.Error(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if draft.TankNo == "" {
		nexus.Error(w, http.StatusBadRequest, "савны дугаар заавал шаардлагатай")
		return
	}
	if _, known := fuelLabels[draft.FuelType]; !known {
		nexus.Error(w, http.StatusBadRequest, "энэ түлшний төрлийг мэдэхгүй байна")
		return
	}
	if draft.CapacityLiters <= 0 {
		nexus.Error(w, http.StatusBadRequest, "багтаамж 0-ээс их байх ёстой")
		return
	}
	if draft.TankType == "" {
		draft.TankType = "vertical_steel"
	}

	// The depot is read first, and by owner, so a tank cannot be attached to
	// somebody else's base by putting their id in the path. The policy alone is
	// not enough: an oversight body can read every depot (`oversight_read`), and
	// a tank it created would count in the national stock.
	var exists bool
	err = m.db.QueryRow(r.Context(),
		`SELECT true FROM petro_depots WHERE id = $1::uuid AND tenant_id = $2::uuid`,
		depotID, tenantID).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		nexus.Error(w, http.StatusNotFound, "ийм бааз олдсонгүй")
		return
	}
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the depot")
		return
	}

	var tank Tank
	err = m.db.QueryRow(r.Context(), `
		INSERT INTO petro_depot_tanks
		       (depot_id, tenant_id, tank_no, tank_type, fuel_type, fuel_label,
		        capacity_liters)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7)
		RETURNING id::text, depot_id::text, tank_no, tank_type, fuel_type, fuel_label,
		          capacity_liters::float8, current_liters::float8, safety_status`,
		depotID, tenantID, draft.TankNo, draft.TankType, draft.FuelType,
		fuelLabel(draft.FuelType), draft.CapacityLiters).
		Scan(&tank.ID, &tank.DepotID, &tank.TankNo, &tank.TankType, &tank.FuelType,
			&tank.FuelLabel, &tank.CapacityLiters, &tank.CurrentLiters, &tank.SafetyStatus)
	if isUniqueViolation(err) {
		nexus.Error(w, http.StatusConflict, "энэ баазад ийм дугаартай сав бүртгэгдсэн байна")
		return
	}
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not register the tank")
		return
	}

	nexus.Audit(r.Context(), tenantID, claims.UserID, "petro.tank.created", tank.ID,
		map[string]any{"depot_id": depotID, "tank_no": tank.TankNo,
			"capacity_liters": tank.CapacityLiters})

	nexus.JSON(w, http.StatusCreated, tank)
}

// GaugeReading is what a measurement records.
//
// Temperature and density and nothing else: the level is not settable here.
// Litres in a tank are the sum of what went in and what came out, and a form
// that could overwrite that sum would make every receipt below it advisory.
type GaugeReading struct {
	TemperatureC *float64 `json:"temperature_c"`
	DensityKgM3  *float64 `json:"density_kg_m3"`
	SafetyStatus string   `json:"safety_status"`
}

// handleUpdateTank records a gauge reading against a vessel.
func (m *Module) handleUpdateTank(w http.ResponseWriter, r *http.Request) {
	if _, ok := nexus.RequireWorkspace(w, r); !ok {
		return
	}
	tankID := chi.URLParam(r, "tankId")
	if !isUUID(tankID) {
		nexus.Error(w, http.StatusBadRequest, "id буруу хэлбэртэй байна")
		return
	}

	var reading GaugeReading
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&reading); err != nil {
		nexus.Error(w, http.StatusBadRequest, "invalid payload")
		return
	}

	var tank Tank
	err := m.db.QueryRow(r.Context(), `
		UPDATE petro_depot_tanks
		   SET temperature_c = COALESCE($2, temperature_c),
		       density_kg_m3 = COALESCE($3, density_kg_m3),
		       safety_status = COALESCE(NULLIF($4, ''), safety_status),
		       updated_at = NOW()
		 WHERE id = $1::uuid
		RETURNING id::text, depot_id::text, tank_no, tank_type, fuel_type, fuel_label,
		          capacity_liters::float8, current_liters::float8,
		          ROUND(current_liters / capacity_liters * 100, 1)::float8,
		          temperature_c::float8, density_kg_m3::float8, safety_status`,
		tankID, reading.TemperatureC, reading.DensityKgM3, reading.SafetyStatus).
		Scan(&tank.ID, &tank.DepotID, &tank.TankNo, &tank.TankType, &tank.FuelType,
			&tank.FuelLabel, &tank.CapacityLiters, &tank.CurrentLiters, &tank.FillPercent,
			&tank.TemperatureC, &tank.DensityKgM3, &tank.SafetyStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		nexus.Error(w, http.StatusNotFound, "ийм сав олдсонгүй")
		return
	}
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not record the reading")
		return
	}

	nexus.JSON(w, http.StatusOK, tank)
}

// ─────────────────────────────────────────────────────────────────────────────
// Unloading a consignment into a tank
// ─────────────────────────────────────────────────────────────────────────────

// DepotReceiveRequest is a consignment going into a vessel.
type DepotReceiveRequest struct {
	TankID         string   `json:"tank_id"`
	ShipmentID     string   `json:"shipment_id"`
	Liters         float64  `json:"liters"`
	ManifestLiters *float64 `json:"manifest_liters"`
	Note           string   `json:"note"`
}

// DepotReceipt is one consignment unloaded.
type DepotReceipt struct {
	ID              string    `json:"id"`
	DepotID         string    `json:"depot_id"`
	TankID          string    `json:"tank_id"`
	ShipmentID      *string   `json:"shipment_id"`
	BatchID         *string   `json:"batch_id"`
	BatchCode       string    `json:"batch_code,omitempty"`
	Liters          float64   `json:"liters"`
	ReceivedAt      time.Time `json:"received_at"`
	TankAfterLiters float64   `json:"tank_after_liters"`
	TankFillPercent float64   `json:"tank_fill_percent"`
}

// handleReceiveIntoDepot unloads a cleared consignment into a tank.
//
// The station's counterpart is receipt.go, and the shape is deliberately the
// same: one transaction holding the receipt, the level, and the state of the
// thing that arrived. What differs is what is checked before the fuel is let
// in — a consignment that customs has not cleared must not appear in a tank,
// because everything downstream treats a tank's contents as lawfully imported.
func (m *Module) handleReceiveIntoDepot(w http.ResponseWriter, r *http.Request) {
	claims, err := nexus.UserFromContext(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	tenantID, ok := nexus.RequireWorkspace(w, r)
	if !ok {
		return
	}
	depotID := chi.URLParam(r, "id")
	if !isUUID(depotID) {
		nexus.Error(w, http.StatusBadRequest, "id буруу хэлбэртэй байна")
		return
	}

	var request DepotReceiveRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
		nexus.Error(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if request.TankID == "" {
		nexus.Error(w, http.StatusBadRequest, "аль сав руу буулгахыг заана уу")
		return
	}
	if request.Liters <= 0 {
		nexus.Error(w, http.StatusBadRequest, "хүлээж авсан литр 0-ээс их байх ёстой")
		return
	}

	tx, err := m.db.Begin(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not start")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()

	// The vessel, locked, and confirmed to belong to the base in the path.
	var tankFuel, tankDepot string
	err = tx.QueryRow(r.Context(), `
		SELECT fuel_type, depot_id::text FROM petro_depot_tanks
		 WHERE id = $1::uuid FOR UPDATE`, request.TankID).Scan(&tankFuel, &tankDepot)
	if errors.Is(err, pgx.ErrNoRows) {
		nexus.Error(w, http.StatusNotFound, "ийм сав олдсонгүй")
		return
	}
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the tank")
		return
	}
	if tankDepot != depotID {
		nexus.Error(w, http.StatusBadRequest, "энэ сав өөр баазынх байна")
		return
	}

	// The consignment, if one was named. A manual receipt without one is
	// allowed — fuel moved between bases has no customs declaration of its own —
	// but a named consignment must be cleared and must be the right grade.
	var (
		shipmentID *string
		batchID    *string
		batchCode  string
	)
	if request.ShipmentID != "" {
		var status, shipmentFuel string
		err = tx.QueryRow(r.Context(), `
			SELECT s.status, s.fuel_type,
			       (SELECT b.id::text FROM petro_batches b WHERE b.customs_shipment_id = s.id LIMIT 1)
			  FROM petro_customs_shipments s
			 WHERE s.id = $1::uuid FOR UPDATE OF s`, request.ShipmentID).
			Scan(&status, &shipmentFuel, &batchID)
		if errors.Is(err, pgx.ErrNoRows) {
			nexus.Error(w, http.StatusNotFound, "ийм мэдүүлэг олдсонгүй")
			return
		}
		if err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not read the shipment")
			return
		}
		if status != "cleared" && status != "in_transit" {
			nexus.Error(w, http.StatusConflict,
				"гаалиар цэвэрлэгдээгүй ачааг баазад хүлээж авах боломжгүй")
			return
		}
		if shipmentFuel != tankFuel {
			nexus.Error(w, http.StatusBadRequest,
				"ачааны түлшний төрөл савныхтай таарахгүй байна")
			return
		}
		shipmentID = &request.ShipmentID
	}

	var receipt DepotReceipt
	err = tx.QueryRow(r.Context(), `
		INSERT INTO petro_depot_receipts
		       (tenant_id, depot_id, tank_id, shipment_id, batch_id,
		        liters, manifest_liters, received_by, note)
		VALUES ($1, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, $7, $8::uuid, $9)
		RETURNING id::text, received_at`,
		tenantID, depotID, request.TankID, shipmentID, batchID,
		request.Liters, request.ManifestLiters, claims.UserID, request.Note).
		Scan(&receipt.ID, &receipt.ReceivedAt)
	if isUniqueViolation(err) {
		nexus.Error(w, http.StatusConflict, "энэ ачааг аль хэдийн баазад хүлээж авсан байна")
		return
	}
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not record the receipt")
		return
	}

	// Into the vessel. `tank_within_capacity` is what refuses an overfill, and
	// letting the database refuse it is the point: the check runs for every
	// writer, including the ones nobody has written yet.
	err = tx.QueryRow(r.Context(), `
		UPDATE petro_depot_tanks
		   SET current_liters = current_liters + $2, updated_at = NOW()
		 WHERE id = $1::uuid
		RETURNING current_liters::float8,
		          ROUND(current_liters / capacity_liters * 100, 1)::float8`,
		request.TankID, request.Liters).
		Scan(&receipt.TankAfterLiters, &receipt.TankFillPercent)
	if isCheckViolation(err) {
		nexus.Error(w, http.StatusConflict, "савны багтаамжид багтахгүй байна")
		return
	}
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not fill the tank")
		return
	}

	// The consignment has arrived. Its journey through the border is over and
	// the litres are now the depot's to account for.
	if shipmentID != nil {
		if _, err := tx.Exec(r.Context(), `
			UPDATE petro_customs_shipments
			   SET status = 'at_depot', updated_at = NOW()
			 WHERE id = $1::uuid`, *shipmentID); err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not close the shipment")
			return
		}
	}

	if batchID != nil {
		if err := tx.QueryRow(r.Context(),
			`SELECT batch_code FROM petro_batches WHERE id = $1::uuid`, *batchID).
			Scan(&batchCode); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			nexus.Error(w, http.StatusInternalServerError, "could not read the batch")
			return
		}
	}

	if err := tx.Commit(r.Context()); err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not save the receipt")
		return
	}

	receipt.DepotID = depotID
	receipt.TankID = request.TankID
	receipt.ShipmentID = shipmentID
	receipt.BatchID = batchID
	receipt.BatchCode = batchCode
	receipt.Liters = request.Liters

	nexus.Audit(r.Context(), tenantID, claims.UserID, "petro.depot.received", receipt.ID,
		map[string]any{"depot_id": depotID, "tank_id": request.TankID,
			"liters": request.Liters, "batch_code": batchCode})

	nexus.JSON(w, http.StatusCreated, receipt)
}

// handleListDepotReceipts is a base's intake history.
func (m *Module) handleListDepotReceipts(w http.ResponseWriter, r *http.Request) {
	if _, ok := nexus.RequireWorkspace(w, r); !ok {
		return
	}
	depotID := chi.URLParam(r, "id")
	if !isUUID(depotID) {
		nexus.Error(w, http.StatusBadRequest, "id буруу хэлбэртэй байна")
		return
	}

	rows, err := m.db.Query(r.Context(), `
		SELECT rc.id::text, rc.depot_id::text, rc.tank_id::text,
		       rc.shipment_id::text, rc.batch_id::text, COALESCE(b.batch_code, ''),
		       rc.liters::float8, rc.received_at
		  FROM petro_depot_receipts rc
		  LEFT JOIN petro_batches b ON b.id = rc.batch_id
		 WHERE rc.depot_id = $1::uuid
		 ORDER BY rc.received_at DESC
		 LIMIT 100`, depotID)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not load the receipts")
		return
	}
	defer rows.Close()

	receipts := []DepotReceipt{}
	for rows.Next() {
		var rc DepotReceipt
		if err := rows.Scan(&rc.ID, &rc.DepotID, &rc.TankID, &rc.ShipmentID,
			&rc.BatchID, &rc.BatchCode, &rc.Liters, &rc.ReceivedAt); err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not read the receipts")
			return
		}
		receipts = append(receipts, rc)
	}
	if rows.Err() != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the receipts")
		return
	}

	nexus.JSON(w, http.StatusOK, map[string]any{"receipts": receipts, "count": len(receipts)})
}

// isCheckViolation reports whether Postgres refused a write because a CHECK
// constraint failed — here, a tank that would go over capacity or below empty.
//
// Distinguished from a unique violation because they mean opposite things to a
// caller: one is "somebody already did this", the other is "this cannot be
// done". Answering 500 to either would tell the operator nothing they can act
// on.
func isCheckViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23514"
}

// isInsufficientPrivilege reports whether one of this module's named actions
// refused its caller.
//
// The SECURITY DEFINER functions raise 42501 when the caller is not who the
// action is for — not an oversight body, or not the forecourt's operator. It is
// a decision the function took deliberately, so it must not reach the caller as
// a 500 alongside the faults nobody planned for.
func isInsufficientPrivilege(err error) bool {
	return hasSQLState(err, "42501")
}

// hasSQLState reports whether err carries the given Postgres error code.
func hasSQLState(err error, code string) bool {
	if err == nil {
		return false
	}
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == code
}
