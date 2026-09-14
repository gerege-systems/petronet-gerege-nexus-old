/*
 * Gerege Nexus
 * Copyright (c) 2026 Gerege Systems Development Team, Gerege Nomadica Foundation
 * Distributed under the Apache 2.0 License.
 *
 * A movement: opened by whoever sends the fuel, closed by whoever receives it.
 *
 * The EU's excise system is built on this one idea and it is the idea worth
 * borrowing. A stock figure is a claim about a moment, and two parties can hold
 * different ones honestly. A movement is an object with a state: it was opened,
 * it named a quantity, and either somebody signed for it or nobody did. An
 * unclosed movement past its due time is not an opinion — it is a question with
 * a name and a number attached.
 *
 * # The reference is national
 *
 * `national_ref` is unique across the whole country rather than per company,
 * because the argument this system exists to settle is between two companies.
 * A reference each side generates in its own numbering is a reference that
 * cannot be quoted in a dispute.
 *
 * # Two numbers, never reconciled into one
 *
 * declared_liters is what left. received_liters is what arrived. The gap is
 * kept as a gap: temperature, a badly calibrated meter, spillage and theft all
 * look identical here, and a system that picked one would be inventing.
 * Correcting both to 15 °C removes the first of those four, which is the only
 * one arithmetic can remove.
 */

package petro

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gerege-systems/open-gerege-nexus/backend/pkg/nexus"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Movement is one consignment on its way.
type Movement struct {
	ID             string   `json:"id"`
	NationalRef    string   `json:"national_ref"`
	FromKind       string   `json:"from_kind"`
	FromID         *string  `json:"from_id"`
	ToKind         string   `json:"to_kind"`
	ToID           *string  `json:"to_id"`
	ProductCode    string   `json:"product_code"`
	DeclaredLiters float64  `json:"declared_liters"`
	ReceivedLiters *float64 `json:"received_liters"`
	Status         string   `json:"status"`
	VariancePct    *float64 `json:"variance_pct"`
	OpenedAt       string   `json:"opened_at"`
	DueAt          *string  `json:"due_at"`
	ClosedAt       *string  `json:"closed_at"`
	Note           string   `json:"note"`
	OverdueByHours *float64 `json:"overdue_by_hours,omitempty"`
}

// MovementDraft opens one.
type MovementDraft struct {
	FromKind       string   `json:"from_kind"`
	FromID         string   `json:"from_id"`
	ToKind         string   `json:"to_kind"`
	ToID           string   `json:"to_id"`
	ProductCode    string   `json:"product_code"`
	DeclaredLiters float64  `json:"declared_liters"`
	TemperatureC   *float64 `json:"temperature_c"`
	DensityKgM3    *float64 `json:"density_kg_m3"`
	TripID         string   `json:"trip_id"`
	DueHours       int      `json:"due_hours"`
	Note           string   `json:"note"`
}

// nationalRef builds a quotable reference: the day it opened and eight hex
// characters. Short enough to read down a telephone, which is how a driver at
// a gate will actually use it.
func nationalRef(now time.Time) string {
	return "MN-" + now.Format("20060102") + "-" +
		strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:8])
}

// handleOpenMovement records that fuel left.
func (m *Module) handleOpenMovement(w http.ResponseWriter, r *http.Request) {
	claims, err := nexus.UserFromContext(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	tenantID, ok := nexus.RequireWorkspace(w, r)
	if !ok {
		return
	}

	var draft MovementDraft
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&draft); err != nil {
		nexus.Error(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if draft.DeclaredLiters <= 0 {
		nexus.Error(w, http.StatusBadRequest, "ачсан хэмжээ 0-ээс их байх ёстой")
		return
	}
	if draft.FromKind == "" || draft.ToKind == "" {
		nexus.Error(w, http.StatusBadRequest, "хаанаас хаашаа явж байгааг заана уу")
		return
	}
	if draft.DueHours <= 0 {
		// Long enough for the far provinces, short enough that a lorry lost for
		// three days is a question the same week.
		draft.DueHours = 72
	}

	categories, err := m.productCategories(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the product dictionary")
		return
	}
	category, known := categories[draft.ProductCode]
	if !known {
		nexus.Error(w, http.StatusBadRequest, "энэ бүтээгдэхүүн тольд байхгүй")
		return
	}

	var declared15C *float64
	if corrected, ok := CorrectToStandard(category, draft.DeclaredLiters, draft.TemperatureC, draft.DensityKgM3); ok {
		declared15C = &corrected
	}

	var trip *string
	if draft.TripID != "" {
		trip = &draft.TripID
	}

	var mv Movement
	err = m.db.QueryRow(r.Context(), `
		INSERT INTO petro_movements
		       (tenant_id, national_ref, from_kind, from_id, to_kind, to_id, product_code,
		        declared_liters, declared_liters_15c, trip_id, due_at, note)
		VALUES ($1, $2, $3, NULLIF($4, '')::uuid, $5, NULLIF($6, '')::uuid, $7,
		        $8, $9, $10::uuid, NOW() + make_interval(hours => $11), $12)
		RETURNING id::text, national_ref, from_kind, from_id::text, to_kind, to_id::text,
		          product_code, declared_liters::float8, received_liters::float8, status,
		          variance_pct::float8, opened_at::text, due_at::text, closed_at::text, note`,
		tenantID, nationalRef(nexus.Now()), draft.FromKind, draft.FromID, draft.ToKind,
		draft.ToID, draft.ProductCode, draft.DeclaredLiters, declared15C, trip,
		draft.DueHours, draft.Note).
		Scan(&mv.ID, &mv.NationalRef, &mv.FromKind, &mv.FromID, &mv.ToKind, &mv.ToID,
			&mv.ProductCode, &mv.DeclaredLiters, &mv.ReceivedLiters, &mv.Status,
			&mv.VariancePct, &mv.OpenedAt, &mv.DueAt, &mv.ClosedAt, &mv.Note)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not open the movement")
		return
	}

	nexus.Audit(r.Context(), tenantID, claims.UserID, "petro.movement.opened", mv.ID,
		map[string]any{"ref": mv.NationalRef, "liters": mv.DeclaredLiters})

	nexus.JSON(w, http.StatusCreated, mv)
}

// MovementReceipt closes one.
type MovementReceipt struct {
	ReceivedLiters float64  `json:"received_liters"`
	TemperatureC   *float64 `json:"temperature_c"`
	DensityKgM3    *float64 `json:"density_kg_m3"`
	Note           string   `json:"note"`
}

// handleCloseMovement records that fuel arrived, and how much of it.
//
// The variance is computed on the corrected volumes when both ends measured,
// and on the observed ones when they did not — with the difference visible in
// the columns, so a gap can never be blamed on temperature without somebody
// having actually recorded a temperature.
func (m *Module) handleCloseMovement(w http.ResponseWriter, r *http.Request) {
	claims, err := nexus.UserFromContext(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	tenantID, ok := nexus.RequireWorkspace(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if !isUUID(id) {
		nexus.Error(w, http.StatusBadRequest, "id буруу хэлбэртэй байна")
		return
	}

	var receipt MovementReceipt
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&receipt); err != nil {
		nexus.Error(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if receipt.ReceivedLiters < 0 {
		nexus.Error(w, http.StatusBadRequest, "хүлээн авсан хэмжээ сөрөг байж болохгүй")
		return
	}

	// Visible to the receiver through `receiver_read`, which is what lets the
	// received litres be corrected with the movement's own product.
	var declared float64
	var product, status string
	err = m.db.QueryRow(r.Context(), `
		SELECT declared_liters::float8, product_code, status
		  FROM petro_movements WHERE id = $1::uuid`, id).
		Scan(&declared, &product, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		nexus.Error(w, http.StatusNotFound, "ийм хөдөлгөөн олдсонгүй")
		return
	}
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the movement")
		return
	}
	if status != "open" {
		nexus.Error(w, http.StatusConflict, "энэ хөдөлгөөн аль хэдийн хаагдсан байна")
		return
	}

	categories, err := m.productCategories(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the product dictionary")
		return
	}

	var received15C *float64
	if corrected, ok := CorrectToStandard(categories[product], receipt.ReceivedLiters,
		receipt.TemperatureC, receipt.DensityKgM3); ok {
		received15C = &corrected
	}

	// Through petro_close_movement rather than an UPDATE here. The row belongs
	// to the sender's organisation, so under `tenant_isolation` the receiver
	// could never touch it and the sender could close its own consignment with
	// whatever figure it liked — the one party with a reason to shrink the gap
	// was the only one able to write it. The function closes a movement only
	// for the organisation that owns its destination, and computes the
	// variance (clamped to ±9999, audit §13) under the same lock as the
	// declared figure it is measured against (migration 00016).
	var mv Movement
	err = m.db.QueryRow(r.Context(), `
		SELECT id::text, national_ref, from_kind, from_id::text, to_kind, to_id::text,
		       product_code, declared_liters::float8, received_liters::float8, status,
		       variance_pct::float8, opened_at::text, due_at::text, closed_at::text, note
		  FROM petro_close_movement($1::uuid, $2, $3, $4::uuid, $5)`,
		id, receipt.ReceivedLiters, received15C, claims.UserID, receipt.Note).
		Scan(&mv.ID, &mv.NationalRef, &mv.FromKind, &mv.FromID, &mv.ToKind, &mv.ToID,
			&mv.ProductCode, &mv.DeclaredLiters, &mv.ReceivedLiters, &mv.Status,
			&mv.VariancePct, &mv.OpenedAt, &mv.DueAt, &mv.ClosedAt, &mv.Note)
	if isInsufficientPrivilege(err) {
		// The sender and the regulator can both read the movement; neither may
		// close it, and which of "not yours" and "not there" is not theirs to
		// learn.
		nexus.Error(w, http.StatusNotFound, "ийм хөдөлгөөн олдсонгүй")
		return
	}
	if hasSQLState(err, "55000") {
		nexus.Error(w, http.StatusConflict, "хөдөлгөөний төлөв өөрчлөгдсөн байна")
		return
	}
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not close the movement")
		return
	}

	nexus.Audit(r.Context(), tenantID, claims.UserID, "petro.movement.closed", mv.ID,
		map[string]any{"ref": mv.NationalRef, "declared": declared,
			"received": receipt.ReceivedLiters, "variance_pct": mv.VariancePct})

	nexus.JSON(w, http.StatusOK, mv)
}

// handleListMovements answers this organisation's consignments, or — for a
// supervisory body — everybody's, because the row-level policy says so.
func (m *Module) handleListMovements(w http.ResponseWriter, r *http.Request) {
	if _, ok := nexus.RequireWorkspace(w, r); !ok {
		return
	}

	status := r.URL.Query().Get("status")
	overdueOnly := r.URL.Query().Get("overdue") == "true"

	rows, err := m.db.Query(r.Context(), `
		SELECT id::text, national_ref, from_kind, from_id::text, to_kind, to_id::text,
		       product_code, declared_liters::float8, received_liters::float8, status,
		       variance_pct::float8, opened_at::text, due_at::text, closed_at::text, note,
		       CASE WHEN status = 'open' AND due_at < NOW()
		            THEN EXTRACT(EPOCH FROM (NOW() - due_at)) / 3600 END::float8
		  FROM petro_movements
		 WHERE ($1 = '' OR status = $1)
		   AND ($2 = false OR (status = 'open' AND due_at < NOW()))
		 ORDER BY opened_at DESC
		 LIMIT 300`, status, overdueOnly)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the movements")
		return
	}
	defer rows.Close()

	out := []Movement{}
	for rows.Next() {
		var mv Movement
		if err := rows.Scan(&mv.ID, &mv.NationalRef, &mv.FromKind, &mv.FromID, &mv.ToKind,
			&mv.ToID, &mv.ProductCode, &mv.DeclaredLiters, &mv.ReceivedLiters, &mv.Status,
			&mv.VariancePct, &mv.OpenedAt, &mv.DueAt, &mv.ClosedAt, &mv.Note,
			&mv.OverdueByHours); err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not read the movements")
			return
		}
		out = append(out, mv)
	}
	if err := rows.Err(); err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the rows")
		return
	}
	nexus.JSON(w, http.StatusOK, map[string]any{"movements": out})
}

// handleDisputeMovement is the regulator marking a consignment as contested.
//
// A separate state from closed: a closed movement with a large variance is a
// fact, and disputing it is a decision somebody took and signed for.
func (m *Module) handleDisputeMovement(w http.ResponseWriter, r *http.Request) {
	tenantID, claims, ok := m.requireOversight(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if !isUUID(id) {
		nexus.Error(w, http.StatusBadRequest, "id буруу хэлбэртэй байна")
		return
	}

	var verdict Verdict
	if r.ContentLength > 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&verdict); err != nil {
			nexus.Error(w, http.StatusBadRequest, "invalid payload")
			return
		}
	}
	if verdict.Note == "" {
		nexus.Error(w, http.StatusBadRequest, "маргаантай гэж үзсэн шалтгааныг бичнэ үү")
		return
	}

	// Through petro_dispute_movement: the policy this replaced was FOR UPDATE on
	// every column, so a regulator could rewrite the declared and received
	// figures it was meant to be disputing (migration 00016).
	var ref *string
	err := m.db.QueryRow(r.Context(),
		`SELECT petro_dispute_movement($1::uuid, $2)`, id, verdict.Note).Scan(&ref)
	if isInsufficientPrivilege(err) {
		nexus.Error(w, http.StatusForbidden, oversightReadOnlyMessage)
		return
	}
	if err == nil && ref == nil {
		nexus.Error(w, http.StatusNotFound, "ийм хөдөлгөөн олдсонгүй, эсвэл маргах боломжгүй төлөвт байна")
		return
	}
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not dispute the movement")
		return
	}

	nexus.Audit(r.Context(), tenantID, claims.UserID, "petro.movement.disputed", id,
		map[string]any{"ref": *ref, "note": verdict.Note})

	nexus.JSON(w, http.StatusOK, map[string]any{"id": id, "national_ref": *ref, "status": "disputed"})
}
