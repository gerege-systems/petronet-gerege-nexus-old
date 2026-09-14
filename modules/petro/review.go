/*
 * Gerege Nexus
 * Copyright (c) 2026 Gerege Systems Development Team, Gerege Nomadica Foundation
 * Distributed under the Apache 2.0 License.
 *
 * The regulator's half: a queue, two verdicts, and the acts that are not
 * verdicts.
 *
 * # Who is the regulator
 *
 * A row in petro_oversight_bodies, not a flag in this file. The row-level
 * policies in migration 00009 read the same table, so a body that can see
 * across companies here can see across them in every query anybody writes
 * later — including the ones written by somebody who never read this comment.
 * The check below is therefore a second lock on the same door: the policy
 * decides what rows exist for the caller, this decides whether the endpoint
 * answers at all, and a mistake in either one is caught by the other.
 *
 * # Approving is not the same as agreeing
 *
 * An approved report is one the state has accepted into the national picture.
 * It is not a statement that the figures are true — nothing here can know that
 * — which is why the balance findings stay attached to an approved submission
 * rather than being cleared by the approval.
 *
 * # Four eyes
 *
 * The approver may not be the submitter. In an organisation where the same
 * person could do both, the review step records nothing at all.
 *
 * # Watching, and acting
 *
 * Suspending a forecourt and closing a movement are not observations. They are
 * the reason this is a control system and not a dashboard, and they are here,
 * next to the review, because they are the same person's job.
 */

package petro

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gerege-systems/open-gerege-nexus/backend/pkg/nexus"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// oversightReadOnlyMessage answers a supervisory body whose scope lets it watch
// but not act.
//
// petro_oversight_bodies.scope was never read: an audit office, a tax or a
// customs body and a province passed petro_is_oversight() exactly as the
// ministry did, and could suspend a forecourt or approve a report. The write
// functions now require 'national' themselves and raise 42501 otherwise — by
// the time a handler sees that code, requireOversight has already passed, so
// the scope is the only thing it can mean (migration 00016).
const oversightReadOnlyMessage = "танай байгууллагын эрх зөвхөн харах — энэ үйлдлийг үндэсний хяналтын байгууллага хийнэ"

// requireOversight refuses a caller whose organisation is not a supervisory
// body, and answers the tenant and user of one that is.
func (m *Module) requireOversight(w http.ResponseWriter, r *http.Request) (tenantID string, claims nexus.UserClaims, ok bool) {
	claims, err := nexus.UserFromContext(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusUnauthorized, "unauthorized")
		return "", claims, false
	}
	tenantID, got := nexus.RequireWorkspace(w, r)
	if !got {
		return "", claims, false
	}

	var isOversight bool
	if err := m.db.QueryRow(r.Context(), `SELECT petro_is_oversight()`).Scan(&isOversight); err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not check the oversight role")
		return "", claims, false
	}
	if !isOversight {
		nexus.Error(w, http.StatusForbidden, "энэ үйлдэл зөвхөн хяналтын байгууллагад нээлттэй")
		return "", claims, false
	}
	return tenantID, claims, true
}

// handleReviewQueue answers what is waiting for a decision.
//
// Ordered by how long it has waited rather than by size: a small company's
// report left for a week is the failure a queue is meant to prevent.
func (m *Module) handleReviewQueue(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := m.requireOversight(w, r); !ok {
		return
	}

	status := r.URL.Query().Get("status")
	if status == "" {
		status = StatusSubmitted
	}

	rows, err := m.db.Query(r.Context(), `
		SELECT s.id::text, s.period_id::text, s.tenant_id::text, t.name, s.version, s.status,
		       s.source,`+submissionCountsSQL+`,
		       s.submitted_at::text, s.reviewed_at::text,`+submissionReviewNoteSQL+`,
		       p.period_start::text, p.period_end::text
		  FROM petro_report_submissions s
		  JOIN petro_report_periods p ON p.id = s.period_id
		  JOIN registry.tenants t ON t.id = s.tenant_id
		 WHERE s.status = $1
		 ORDER BY s.submitted_at
		 LIMIT 500`, status)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the queue")
		return
	}
	defer rows.Close()

	out := []Submission{}
	for rows.Next() {
		var s Submission
		if err := rows.Scan(&s.ID, &s.PeriodID, &s.TenantID, &s.TenantName, &s.Version,
			&s.Status, &s.Source, &s.RowCount, &s.ErrorCount, &s.WarningCount,
			&s.SubmittedAt, &s.ReviewedAt, &s.ReviewNote,
			&s.PeriodStart, &s.PeriodEnd); err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not read the queue")
			return
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the rows")
		return
	}
	nexus.JSON(w, http.StatusOK, map[string]any{"submissions": out})
}

// Verdict is what an official writes when they decide.
type Verdict struct {
	Note string `json:"note"`
}

// handleReview records an approval or a return.
//
// The decision is taken from the path so that the two verdicts cannot be
// swapped by a client sending a different body than it meant to.
func (m *Module) handleReview(decision string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&verdict); err != nil {
				nexus.Error(w, http.StatusBadRequest, "invalid payload")
				return
			}
		}
		if decision == StatusReturned && verdict.Note == "" {
			nexus.Error(w, http.StatusBadRequest, "буцаах шалтгааныг бичнэ үү")
			return
		}
		if noteTooLong(verdict.Note) {
			nexus.Error(w, http.StatusBadRequest, noteTooLongMessage)
			return
		}

		// Four eyes, and the state machine, in one statement: a submission
		// already decided is not decided again, and the person who sent it is
		// not the person who accepts it.
		var submittedBy *string
		var previous string
		err := m.db.QueryRow(r.Context(), `
			SELECT COALESCE(submitted_by::text, ''), status
			  FROM petro_report_submissions WHERE id = $1::uuid`, id).
			Scan(&submittedBy, &previous)
		if errors.Is(err, pgx.ErrNoRows) {
			nexus.Error(w, http.StatusNotFound, "ийм тайлан олдсонгүй")
			return
		}
		if err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not read the submission")
			return
		}
		if previous != StatusSubmitted {
			nexus.Error(w, http.StatusConflict, "энэ тайлан хүлээгдэж буй төлөвт байхгүй байна")
			return
		}
		if submittedBy != nil && *submittedBy == claims.UserID {
			nexus.Error(w, http.StatusForbidden, "илгээсэн хүн өөрөө батлахгүй")
			return
		}

		// Through petro_review_submission rather than an UPDATE under the old
		// `oversight_review` policy, which was FOR UPDATE on every column: the
		// hash, the chain position, the tenant and the declared figures were
		// all writable by the body that was only meant to decide. The function
		// writes the decision columns alone and repeats both guards above in
		// its WHERE, so a race between two officials still decides once
		// (migration 00016).
		var updated Submission
		err = m.db.QueryRow(r.Context(), `
			SELECT id::text, period_id::text, version, status, source, row_count,
			       error_count, warning_count, submitted_at::text, reviewed_at::text, review_note
			  FROM petro_review_submission($1::uuid, $2, $3, $4::uuid)`,
			id, decision, verdict.Note, claims.UserID).
			Scan(&updated.ID, &updated.PeriodID, &updated.Version, &updated.Status,
				&updated.Source, &updated.RowCount, &updated.ErrorCount, &updated.WarningCount,
				&updated.SubmittedAt, &updated.ReviewedAt, &updated.ReviewNote)
		if isInsufficientPrivilege(err) {
			nexus.Error(w, http.StatusForbidden, oversightReadOnlyMessage)
			return
		}
		if errors.Is(err, pgx.ErrNoRows) {
			nexus.Error(w, http.StatusConflict, "тайлангийн төлөв өөрчлөгдсөн байна")
			return
		}
		if err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not record the decision")
			return
		}

		nexus.Audit(r.Context(), tenantID, claims.UserID, "petro.report."+decision, id,
			map[string]any{"note": verdict.Note, "version": updated.Version})

		nexus.JSON(w, http.StatusOK, updated)
	}
}

// SiteStatusChange suspends a site, or lifts a suspension.
//
// The register status, not the stock status: a suspended forecourt may be full
// of fuel, and conflating the two would let a suspension be argued away with a
// delivery note.
type SiteStatusChange struct {
	RegistryStatus string `json:"registry_status"`
	Note           string `json:"note"`
}

var siteStatuses = map[string]bool{"active": true, "suspended": true, "closed": true}

// handleSetSiteStatus is the regulator acting rather than watching.
func (m *Module) handleSetSiteStatus(w http.ResponseWriter, r *http.Request) {
	tenantID, claims, ok := m.requireOversight(w, r)
	if !ok {
		return
	}
	kind := chi.URLParam(r, "kind")
	id := chi.URLParam(r, "id")
	if !isUUID(id) {
		nexus.Error(w, http.StatusBadRequest, "id буруу хэлбэртэй байна")
		return
	}

	var change SiteStatusChange
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&change); err != nil {
		nexus.Error(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if !siteStatuses[change.RegistryStatus] {
		nexus.Error(w, http.StatusBadRequest, "статус нь active, suspended, closed гурвын нэг байна")
		return
	}

	if kind != "station" && kind != "depot" {
		nexus.Error(w, http.StatusBadRequest, "объектын төрөл нь station эсвэл depot байна")
		return
	}

	// Through a named action rather than an UPDATE under a row-level policy.
	//
	// The policy that used to carry this was `FOR UPDATE`, and a row-level
	// policy knows nothing about columns: it opened every column of every
	// company's register to any manager inside a supervisory body — a name, a
	// coordinate, a licence number, all writable by somebody who was only ever
	// meant to be able to suspend. The function does the one thing and checks
	// petro_is_oversight() itself (migration 00011).
	var name *string
	err := m.db.QueryRow(r.Context(),
		`SELECT petro_set_site_status($1, $2::uuid, $3)`, kind, id, change.RegistryStatus).
		Scan(&name)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42501" {
			nexus.Error(w, http.StatusForbidden, oversightReadOnlyMessage)
			return
		}
		nexus.Error(w, http.StatusInternalServerError, "could not change the status")
		return
	}
	if name == nil {
		nexus.Error(w, http.StatusNotFound, "ийм объект олдсонгүй")
		return
	}

	nexus.Audit(r.Context(), tenantID, claims.UserID, "petro.site."+change.RegistryStatus, id,
		map[string]any{"kind": kind, "name": *name, "note": change.Note})

	nexus.JSON(w, http.StatusOK, map[string]any{
		"id": id, "kind": kind, "name": *name, "registry_status": change.RegistryStatus,
	})
}

// isOversightTenant answers the same question as requireOversight without
// writing a response, for the paths that only need to shape an answer.
func (m *Module) isOversightTenant(ctx context.Context) bool {
	var ok bool
	if err := m.db.QueryRow(ctx, `SELECT petro_is_oversight()`).Scan(&ok); err != nil {
		return false
	}
	return ok
}
