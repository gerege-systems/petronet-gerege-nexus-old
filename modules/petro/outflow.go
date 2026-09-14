/*
 * Gerege Nexus
 * Copyright (c) 2026 Gerege Systems Development Team, Gerege Nomadica Foundation.
 * Distributed under the Apache 2.0 License.
 *
 * Түлш гарах хоёр зам — гинжний дутуу байсан хасах тэмдэг.
 *
 * # Юу дутуу байсан бэ
 *
 * Аудитын №1: модуль дотор нөөцийн бичилт бүр НЭМЭХ үйлдэл байв. Агуулахын
 * сав `+`, ШТС-ийн сав `+`, партийн хүлээн авалт `+`. `petro_depot_tanks` ба
 * `petro_station_inventory`-оос хасах ганц ч мэдэгдэл репод байгаагүй. Үр дүн
 * нь: агуулахад 100,000 л хүлээж авна → сав 100,000. Тэндээс рейс гарч ШТС
 * дээр 100,000 л хүлээж авна → ШТС 100,000, агуулах ХЭВЭЭР 100,000. Улсын
 * нөөц гэж 200,000 л мэдээлэгдэнэ — систем оршин байгаагийн гол шалтгаан
 * болох тэр тоо хоёр дахин.
 *
 * # Хоёр үйл явдал, хоёулаа нэг зарчимтай
 *
 *	ачилт    агуулахын савнаас хасаж, рейс үүсгэнэ
 *	түгээлт  ШТС-ийн савнаас хасаж, ваучерыг хаана
 *
 * Хоёулаа транзакц: сав буурсан атлаа рейс үүсээгүй бол түлш алга болно, рейс
 * үүссэн атлаа сав буураагүй бол түлш хоёр дахин болно. Аль нь ч болохгүй.
 *
 * # Сөрөг үлдэгдлийг өгөгдлийн сан татгалзана
 *
 * `tank_within_capacity` CHECK нь 0-ээс доош унахыг аль хэдийн хориглодог —
 * агуулахын хувьд шалгалт нь энд биш тэнд. ШТС-ийн хүснэгтэд тийм CHECK
 * байгаагүй тул 00012 түүнийг нэмнэ (аудитын №8). Хоёр тохиолдолд ч handler
 * эхлээд уншаад дараа нь бичдэггүй: хоёр зэрэг түгээлт хоёулаа «хангалттай
 * байна» гэж уншаад хоёулаа хасах болно.
 */

package petro

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gerege-systems/open-gerege-nexus/backend/pkg/nexus"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// DispatchDraft loads a tanker at a depot.
type DispatchDraft struct {
	TankID       string  `json:"tank_id"`
	ToStationID  string  `json:"to_station_id"`
	Liters       float64 `json:"liters"`
	TankerPlate  string  `json:"tanker_plate"`
	DriverName   string  `json:"driver_name"`
	DriverPhone  string  `json:"driver_phone"`
	SealNo       string  `json:"seal_no"`
	TripCode     string  `json:"trip_code"`
	OpenMovement bool    `json:"open_movement"`
}

// Dispatch is a loaded tanker on its way.
type Dispatch struct {
	TripID          string  `json:"trip_id"`
	TripCode        string  `json:"trip_code"`
	FuelType        string  `json:"fuel_type"`
	Liters          float64 `json:"liters"`
	TankAfterLiters float64 `json:"tank_after_liters"`
	MovementRef     string  `json:"movement_ref,omitempty"`
	ToStationID     string  `json:"to_station_id,omitempty"`
}

// handleDispatchFromDepot takes fuel out of a depot tank and puts it on a lorry.
//
// This is the event the chain was missing. Until it existed the only way a
// depot's level could change was upward, so every litre that left a base was
// still counted there — and counted again at the forecourt it reached.
func (m *Module) handleDispatchFromDepot(w http.ResponseWriter, r *http.Request) {
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

	var draft DispatchDraft
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&draft); err != nil {
		nexus.Error(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if !isUUID(draft.TankID) {
		nexus.Error(w, http.StatusBadRequest, "савны id буруу")
		return
	}
	if draft.Liters <= 0 {
		nexus.Error(w, http.StatusBadRequest, "ачих хэмжээ 0-ээс их байх ёстой")
		return
	}
	if draft.ToStationID != "" && !isUUID(draft.ToStationID) {
		nexus.Error(w, http.StatusBadRequest, "ШТС-ийн id буруу")
		return
	}
	if draft.TankerPlate == "" {
		nexus.Error(w, http.StatusBadRequest, "цистерний улсын дугаар заавал")
		return
	}
	// A movement is closed by whoever owns its destination (migration 00016),
	// so one opened with no station is refused by the table's CHECK after the
	// tank has been drawn down — answered here, before anything moves.
	if draft.OpenMovement && draft.ToStationID == "" {
		nexus.Error(w, http.StatusBadRequest, "хөдөлгөөн нээхэд очих ШТС-ийг заана уу")
		return
	}

	// The destination has to be a forecourt that exists and may trade. The
	// foreign key alone did not say so: it is checked past the row-level
	// policy, so a load could name any company's station — including a
	// suspended one — and nobody could then receive it, leaving the litres
	// drawn from the tank and on the road for ever. Asked through a named
	// function because another company's station is not a row this session can
	// read (migration 00016).
	if draft.ToStationID != "" {
		var registry *string
		if err := m.db.QueryRow(r.Context(),
			`SELECT petro_station_registry_status($1::uuid)`, draft.ToStationID).
			Scan(&registry); err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not check the station")
			return
		}
		if registry == nil {
			nexus.Error(w, http.StatusNotFound, "ийм ШТС олдсонгүй")
			return
		}
		if *registry != "active" {
			nexus.Error(w, http.StatusConflict, "энэ ШТС түдгэлзсэн эсвэл хаагдсан тул ачаа хүлээж авахгүй")
			return
		}
	}

	tx, err := m.db.Begin(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not open a transaction")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()

	// Out of the tank first, and by subtraction rather than by assignment: two
	// lorries loading at once must not both read the same level and both
	// succeed. The CHECK constraint is what refuses the overdraw, so the
	// decision is the database's and applies to every future caller too.
	var out Dispatch
	err = tx.QueryRow(r.Context(), `
		UPDATE petro_depot_tanks
		   SET current_liters = current_liters - $3, updated_at = NOW()
		 WHERE id = $1::uuid AND depot_id = $2::uuid
		RETURNING fuel_type, fuel_label, current_liters::float8`,
		draft.TankID, depotID, draft.Liters).
		Scan(&out.FuelType, new(string), &out.TankAfterLiters)
	if errors.Is(err, pgx.ErrNoRows) {
		nexus.Error(w, http.StatusNotFound, "энэ баазад ийм сав олдсонгүй")
		return
	}
	if isCheckViolation(err) {
		nexus.Error(w, http.StatusConflict, "савны үлдэгдэл хүрэлцэхгүй байна")
		return
	}
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not draw from the tank")
		return
	}
	out.Liters = draft.Liters

	// The load carries its batch only when the tank has only ever held one, so
	// a forecourt's receipt adds to the right importer's total. A tank that has
	// blended two batches, or took fuel with none, carries NULL: receipts do not
	// record the level they topped up, so the blend cannot be apportioned, and
	// crediting all of it to the latest batch would be a confident wrong answer.
	var batchID *string
	if err := tx.QueryRow(r.Context(), `
		SELECT CASE WHEN COUNT(*) > 0
		             AND COUNT(*) = COUNT(batch_id)
		             AND COUNT(DISTINCT batch_id) = 1
		            THEN (array_agg(batch_id))[1]::text END
		  FROM petro_depot_receipts
		 WHERE tank_id = $1::uuid`, draft.TankID).Scan(&batchID); err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the tank's batch")
		return
	}

	code := draft.TripCode
	if code == "" {
		code = nationalRef(nexus.Now())
	}
	out.TripCode = code

	var station *string
	if draft.ToStationID != "" {
		station = &draft.ToStationID
		out.ToStationID = draft.ToStationID
	}

	err = tx.QueryRow(r.Context(), `
		INSERT INTO petro_dispatch_trips
		       (tenant_id, trip_code, tanker_plate, driver_name, driver_phone,
		        to_station_id, fuel_type, fuel_label, volume_liters, seal_no,
		        from_depot_id, from_tank_id, batch_id, status)
		VALUES ($1, $2, $3, $4, $5, $6::uuid, $7, $8, $9, $10, $11::uuid, $12::uuid, $13::uuid, 'in_transit')
		RETURNING id::text`,
		tenantID, code, draft.TankerPlate, draft.DriverName, draft.DriverPhone,
		station, out.FuelType, fuelLabel(out.FuelType), draft.Liters, draft.SealNo,
		depotID, draft.TankID, batchID).Scan(&out.TripID)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not record the trip")
		return
	}

	// One act, one movement — the object the regulator can chase. Optional
	// because a company may not be registering movements yet, and a load that
	// happened is worth recording either way.
	if draft.OpenMovement {
		ref := nationalRef(nexus.Now())
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO petro_movements
			       (tenant_id, national_ref, from_kind, from_id, to_kind, to_id,
			        product_code, declared_liters, trip_id, due_at)
			VALUES ($1, $2, 'depot', $3::uuid, 'station', $4::uuid, $5, $6, $7::uuid,
			        NOW() + INTERVAL '72 hours')`,
			tenantID, ref, depotID, station, out.FuelType, draft.Liters, out.TripID); err != nil {
			nexus.Error(w, http.StatusBadRequest, "could not open the movement")
			return
		}
		out.MovementRef = ref
	}

	if err := tx.Commit(r.Context()); err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not commit the dispatch")
		return
	}

	nexus.Audit(r.Context(), tenantID, claims.UserID, "petro.depot.dispatched", out.TripID,
		map[string]any{"depot_id": depotID, "tank_id": draft.TankID,
			"liters": draft.Liters, "plate": draft.TankerPlate})

	nexus.JSON(w, http.StatusCreated, out)
}

// SaleDraft is fuel leaving a forecourt.
//
// VoucherID is optional: most litres in this country are bought with money.
// When it is present the sale also closes the voucher, which is the only place
// in the system that ever does — see handleRecordSale.
type SaleDraft struct {
	FuelType  string  `json:"fuel_type"`
	Liters    float64 `json:"liters"`
	VoucherID string  `json:"voucher_id"`
	QRToken   string  `json:"qr_token"`
	Note      string  `json:"note"`
}

// Sale is what the forecourt is left with.
type Sale struct {
	StationID       string   `json:"station_id"`
	FuelType        string   `json:"fuel_type"`
	Liters          float64  `json:"liters"`
	StockAfterLiter float64  `json:"stock_after_liters"`
	VoucherID       string   `json:"voucher_id,omitempty"`
	AmountMNT       *float64 `json:"amount_mnt,omitempty"`
}

// handleRecordSale takes fuel out of a forecourt tank, and closes a voucher if
// one was presented.
//
// # Why the voucher is redeemed here and nowhere else
//
// Audit §2: the columns redeemed_at, redeemed_station_id and redeemed_liters
// existed, handleMyVouchers read them, and nothing in the repository ever
// wrote them. A voucher's only ending was expiry, and expiry gives the amount
// back — so a citizen could draw fifty thousand tugrik, buy the fuel, and have
// the entitlement returned to them three hours later, every three hours. The
// daily limit existed only on paper.
//
// A voucher is spent at a pump, so the pump's own act — the sale — is what
// closes it. A separate "redeem" endpoint would let a voucher be closed
// without fuel moving, and fuel move without a voucher closing.
func (m *Module) handleRecordSale(w http.ResponseWriter, r *http.Request) {
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
		nexus.Error(w, http.StatusBadRequest, "ШТС-ийн id буруу")
		return
	}

	var draft SaleDraft
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&draft); err != nil {
		nexus.Error(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if draft.Liters <= 0 {
		nexus.Error(w, http.StatusBadRequest, "түгээсэн хэмжээ 0-ээс их байх ёстой")
		return
	}
	if draft.VoucherID != "" && !isUUID(draft.VoucherID) {
		nexus.Error(w, http.StatusBadRequest, "ваучерын id буруу")
		return
	}
	// The id alone used to be enough, and an id is not a secret: it is written
	// to the audit log with every sale. The code the citizen shows is what
	// spends the voucher; the id, when sent, only has to agree with it
	// (migration 00016).
	if draft.VoucherID != "" && draft.QRToken == "" {
		nexus.Error(w, http.StatusBadRequest, "ваучерын QR код заавал")
		return
	}

	tx, err := m.db.Begin(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not open a transaction")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()

	sale := Sale{StationID: stationID, Liters: draft.Liters}

	// The voucher first: if it cannot be closed, no fuel should leave. Matched
	// by the QR the citizen presents (and the id, when sent), only while active —
	// the WHERE clause is what makes a second scan of the same code fail
	// rather than dispense twice.
	//
	// Through petro_redeem_voucher rather than an UPDATE here, and the reason
	// is that the UPDATE could never touch the row it was written for. A
	// citizen belongs to the eID organisation and a forecourt to its operator,
	// so `tenant_isolation` narrowed the statement to a table with none of that
	// citizen's vouchers in it: zero rows, no error, and the 409 below —
	// indistinguishable, in production, from a code that really had been spent.
	// The function is SECURITY DEFINER and checks the one thing that matters
	// instead: that the forecourt dispensing the fuel is the caller's own
	// (migration 00014).
	if draft.VoucherID != "" || draft.QRToken != "" {
		var amount float64
		var fuelType string
		err = tx.QueryRow(r.Context(), `
			SELECT voucher_id::text, voucher_amount::float8, voucher_fuel
			  FROM petro_redeem_voucher(
			           NULLIF($1, '')::uuid, NULLIF($4, ''), $2::uuid, $3)`,
			draft.VoucherID, stationID, draft.Liters, draft.QRToken).
			Scan(&sale.VoucherID, &amount, &fuelType)
		if errors.Is(err, pgx.ErrNoRows) {
			nexus.Error(w, http.StatusConflict,
				"ваучер идэвхгүй, хугацаа дууссан, эсвэл аль хэдийн ашиглагдсан байна")
			return
		}
		if isInsufficientPrivilege(err) {
			// The same answer the inventory statement below gives for a
			// forecourt this organisation cannot see: which of "not yours" and
			// "not there" it is, is not the caller's to learn.
			nexus.Error(w, http.StatusNotFound, "ШТС олдсонгүй")
			return
		}
		if hasSQLState(err, "55000") {
			nexus.Error(w, http.StatusConflict,
				"энэ ШТС түдгэлзсэн эсвэл ваучер хүлээн авдаггүй байна")
			return
		}
		if err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not redeem the voucher")
			return
		}
		sale.AmountMNT = &amount
		if draft.FuelType == "" {
			draft.FuelType = fuelType
		}
		if draft.FuelType != fuelType {
			nexus.Error(w, http.StatusConflict, "ваучерын түлшний төрөл таарахгүй байна")
			return
		}
	}

	if draft.FuelType == "" {
		nexus.Error(w, http.StatusBadRequest, "түлшний төрөл заавал")
		return
	}
	sale.FuelType = draft.FuelType

	// Out of the tank, by subtraction. The CHECK added in 00012 refuses a
	// forecourt dispensing more than it holds.
	err = tx.QueryRow(r.Context(), `
		UPDATE petro_station_inventory
		   SET current_stock_liters = current_stock_liters - $3, last_reported_at = NOW()
		 WHERE station_id = $1::uuid AND fuel_type = $2
		RETURNING current_stock_liters::float8`,
		stationID, draft.FuelType, draft.Liters).Scan(&sale.StockAfterLiter)
	if errors.Is(err, pgx.ErrNoRows) {
		nexus.Error(w, http.StatusNotFound, "энэ ШТС дээр ийм түлш бүртгэлгүй байна")
		return
	}
	if isCheckViolation(err) {
		nexus.Error(w, http.StatusConflict, "савны үлдэгдэл хүрэлцэхгүй байна")
		return
	}
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not draw from the tank")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not commit the sale")
		return
	}

	nexus.Audit(r.Context(), tenantID, claims.UserID, "petro.station.dispensed", stationID,
		map[string]any{"fuel_type": draft.FuelType, "liters": draft.Liters,
			"voucher_id": sale.VoucherID})

	nexus.JSON(w, http.StatusCreated, sale)
}
