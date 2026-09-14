/*
 * Gerege Nexus
 * Copyright (c) 2026 Gerege Systems Development Team, Gerege Nomadica Foundation
 * Distributed under the Apache 2.0 License.
 *
 * Taking a delivery, and the only place stock goes up.
 *
 * Until this existed a tanker arrived, its run was marked completed, and the
 * forecourt's tank held exactly what it held before. Twelve thousand litres
 * went nowhere anybody could point at. The chain the whole platform is for —
 * border, depot, road, pump — had no last link, and the stock figures were
 * whatever the importer seeded.
 *
 * # One receipt per run
 *
 * Enforced by a partial unique index rather than by looking first. Two taps on
 * a phone with a poor signal are two requests, and a check followed by an
 * insert lets both through — which would add the load twice and quietly break
 * the one number the regulator is watching.
 *
 * # What the difference means
 *
 * `manifest_liters` is what the paperwork says; `liters` is what the attendant
 * measured. This records both and reconciles neither. A gap is loss, theft, or
 * a badly calibrated gauge, and nothing here can tell which — but a system that
 * stored only one figure could not even say a gap existed.
 */

package petro

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gerege-systems/open-gerege-nexus/backend/pkg/nexus"
	"github.com/go-chi/chi/v5"
)

// ReceiveRequest is what a station says when a tanker is unloaded.
type ReceiveRequest struct {
	// Liters measured into the tank. Required: it is the whole point.
	Liters float64 `json:"liters"`
	// ManifestLiters is what the delivery note claims, when it differs.
	ManifestLiters *float64 `json:"manifest_liters"`
	// SealStatus at arrival — sealed_intact, opened_authorized, seal_tampered.
	SealStatus string `json:"seal_status"`
	Note       string `json:"note"`
}

// Receipt is one delivery, recorded.
type Receipt struct {
	ID          string    `json:"id"`
	StationID   string    `json:"station_id"`
	StationName string    `json:"station_name"`
	TripID      *string   `json:"trip_id"`
	BatchID     *string   `json:"batch_id"`
	BatchCode   string    `json:"batch_code,omitempty"`
	FuelType    string    `json:"fuel_type"`
	FuelLabel   string    `json:"fuel_label"`
	Liters      float64   `json:"liters"`
	SealStatus  string    `json:"seal_status"`
	ReceivedAt  time.Time `json:"received_at"`
	// StockAfter is the tank level once this load is in it, so the screen that
	// confirmed the delivery can show the result rather than asking again.
	StockAfterLiters float64 `json:"stock_after_liters"`
}

// The seal states a station may report.
//
// A closed list, because this is the field a fraud case turns on: an attendant
// under pressure types what they are told to type, and free text would make
// "tampered" and "tamperd" two different facts.
var sealStates = map[string]bool{
	"sealed_intact":     true,
	"opened_authorized": true,
	"seal_tampered":     true,
}

// handleReceiveDelivery unloads a tanker into a forecourt's tank.
//
// Everything in one transaction: the receipt, the stock, the run's completion
// and the batch's running total. A half-applied delivery is a forecourt that
// has the fuel and no record of where it came from — which is the exact state
// this feature exists to make impossible.
func (m *Module) handleReceiveDelivery(w http.ResponseWriter, r *http.Request) {
	claims, err := nexus.UserFromContext(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	tenantID, ok := nexus.RequireWorkspace(w, r)
	if !ok {
		return
	}
	tripID := chi.URLParam(r, "id")
	if !isUUID(tripID) {
		nexus.Error(w, http.StatusBadRequest, "id буруу хэлбэртэй байна")
		return
	}

	var request ReceiveRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
		nexus.Error(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if request.Liters <= 0 {
		nexus.Error(w, http.StatusBadRequest, "хүлээж авсан литр 0-ээс их байх ёстой")
		return
	}
	if request.SealStatus == "" {
		request.SealStatus = "sealed_intact"
	}
	if !sealStates[request.SealStatus] {
		nexus.Error(w, http.StatusBadRequest, "лацны төлөв танигдахгүй байна")
		return
	}
	if noteTooLong(request.Note) {
		nexus.Error(w, http.StatusBadRequest, noteTooLongMessage)
		return
	}

	// One call, one transaction: the receipt, the stock, the run's completion
	// and the batch's running total, all inside petro_receive_trip.
	//
	// Through a function because those four rows belong to three different
	// organisations whenever a depot company supplies somebody else's
	// forecourt: the run to the sender, the batch to the importer, the tank and
	// the receipt to the station. Under `tenant_isolation` the station could
	// not see the run (404), and the sender receiving on the station's behalf
	// failed at the tank (500) after its own depot had already been drawn down
	// — litres on the road for ever. The function acts only for the
	// organisation that owns the destination station, and writes the receipt
	// and the stock under that organisation (migration 00016).
	//
	// The trip row is locked inside, so two attendants confirming the same
	// arrival at once queue behind each other, and the unique index on trip_id
	// refuses the second receipt.
	var (
		receipt   Receipt
		stationID string
		batchID   *string
		batchCode *string
	)
	err = m.db.QueryRow(r.Context(), `
		SELECT receipt_id::text, receipt_at, receipt_station::text,
		       COALESCE(receipt_station_name, ''), trip_fuel, trip_fuel_label,
		       trip_batch::text, trip_batch_code, stock_after::float8
		  FROM petro_receive_trip($1::uuid, $2, $3, $4, $5::uuid, $6)`,
		tripID, request.Liters, request.ManifestLiters, request.SealStatus,
		claims.UserID, request.Note).
		Scan(&receipt.ID, &receipt.ReceivedAt, &stationID, &receipt.StationName,
			&receipt.FuelType, &receipt.FuelLabel, &batchID, &batchCode,
			&receipt.StockAfterLiters)
	if isInsufficientPrivilege(err) {
		nexus.Error(w, http.StatusNotFound, "ийм рейс олдсонгүй")
		return
	}
	if hasSQLState(err, "55000") {
		// A station deleted while its delivery was on the road leaves
		// to_station_id NULL (ON DELETE SET NULL, audit §34).
		nexus.Error(w, http.StatusConflict, "энэ рейс аль ШТС рүү явахыг заагаагүй байна")
		return
	}
	if isUniqueViolation(err) {
		nexus.Error(w, http.StatusConflict, "энэ рейсийг аль хэдийн хүлээж авсан байна")
		return
	}
	// Only the capacity constraint is the tank being full. Any other CHECK is
	// a value the request should not have carried.
	if isCheckViolation(err) && violatedConstraint(err) == "station_stock_within_capacity" {
		nexus.Error(w, http.StatusConflict, "ШТС-ийн савны багтаамж хүрэлцэхгүй байна")
		return
	}
	if isCheckViolation(err) {
		nexus.Error(w, http.StatusBadRequest, "хүлээн авалтын утга зөвшөөрөгдөөгүй байна")
		return
	}
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not record the delivery")
		return
	}
	if batchCode != nil {
		receipt.BatchCode = *batchCode
	}

	receipt.StationID = stationID
	receipt.TripID = &tripID
	receipt.BatchID = batchID
	receipt.Liters = request.Liters
	receipt.SealStatus = request.SealStatus

	nexus.Audit(r.Context(), tenantID, claims.UserID, "petro.delivery.received", receipt.ID,
		map[string]any{
			"station_id":  stationID,
			"liters":      request.Liters,
			"seal_status": request.SealStatus,
			"batch_code":  receipt.BatchCode,
		})

	nexus.JSON(w, http.StatusCreated, receipt)
}

// handleListReceipts is a forecourt's delivery history.
func (m *Module) handleListReceipts(w http.ResponseWriter, r *http.Request) {
	if _, ok := nexus.RequireWorkspace(w, r); !ok {
		return
	}
	stationID := chi.URLParam(r, "id")
	if !isUUID(stationID) {
		nexus.Error(w, http.StatusBadRequest, "id буруу хэлбэртэй байна")
		return
	}

	rows, err := m.db.Query(r.Context(), `
		SELECT rc.id::text, rc.station_id::text, COALESCE(s.name, ''),
		       rc.trip_id::text, rc.batch_id::text, COALESCE(b.batch_code, ''),
		       rc.fuel_type, COALESCE(b.fuel_label, rc.fuel_type),
		       rc.liters::float8, rc.seal_status, rc.received_at
		  FROM petro_station_receipts rc
		  LEFT JOIN petro_stations s ON s.id = rc.station_id
		  LEFT JOIN petro_batches b ON b.id = rc.batch_id
		 WHERE rc.station_id = $1::uuid
		 ORDER BY rc.received_at DESC
		 LIMIT 100`, stationID)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not load the deliveries")
		return
	}
	defer rows.Close()

	receipts := []Receipt{}
	for rows.Next() {
		var rc Receipt
		if err := rows.Scan(&rc.ID, &rc.StationID, &rc.StationName,
			&rc.TripID, &rc.BatchID, &rc.BatchCode,
			&rc.FuelType, &rc.FuelLabel, &rc.Liters, &rc.SealStatus, &rc.ReceivedAt); err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not read the deliveries")
			return
		}
		receipts = append(receipts, rc)
	}
	if rows.Err() != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the deliveries")
		return
	}

	nexus.JSON(w, http.StatusOK, map[string]any{"receipts": receipts, "count": len(receipts)})
}

// isUniqueViolation reports whether an error is Postgres refusing a duplicate.
//
// By SQLSTATE rather than by message: the message is localised and the code is
// not, and this one distinguishes "somebody already did this" from "the
// database is broken" — two answers a caller must not confuse.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23505"
}

// isUUID says whether a path or body value can be cast to uuid at all.
//
// Ten places used to interpolate `$1::uuid` straight from a URL, so a request
// for /depots/not-a-uuid/receipts answered 500 rather than 400 — an invalid
// request reported as a server fault (audit §35). Checking the shape here is
// cheaper than teaching every query to recognise 22P02.
func isUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, c := range value {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}
