/*
 * Gerege Nexus
 * Copyright (c) 2026 Gerege Systems Development Team, Gerege Nomadica Foundation
 * Distributed under the Apache 2.0 License.
 *
 * The company's half of the regulatory loop: a period, a submission, its lines.
 *
 * Everything above this file models fuel the operator moved themselves. This
 * one models fuel somebody else moved and is now telling the state about — two
 * hundred companies, eleven hundred forecourts, one figure per grade per day.
 * It is the only door data comes in through, so the door is the design.
 *
 * # A submission is returned by the system, not by a person
 *
 * If a line breaks a rule the submission is stored anyway, marked `returned`,
 * with a finding per broken rule. Nothing is thrown away and nobody waits for
 * an official to notice: the sender sees which line and which rule within a
 * second of pressing send. The regulator's queue then holds only submissions
 * that already passed arithmetic, which is what makes one official able to
 * review two hundred companies.
 *
 * # Versions, not edits
 *
 * A corrected report is version 2. Version 1 stays, with its findings, and the
 * pair of them is the audit trail: what was claimed first, what was wrong with
 * it, what was claimed instead. A system that let a sender overwrite yesterday
 * could not answer the only question an inspector ever asks.
 *
 * # The hash chain
 *
 * Each submission carries the hash of the one before it from the same company.
 * That is not cryptography for its own sake — it is the cheapest available
 * answer to "was this figure changed after the fact", and it costs one column
 * and one SHA-256 per submission. A gap in the sequence or a hash that stops
 * matching is a question somebody has to answer; without the chain the same
 * edit leaves no trace at all.
 */

package petro

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gerege-systems/open-gerege-nexus/backend/pkg/nexus"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// Submission states. `returned` is written by the system on a failed
// validation and by the regulator on a refusal; the two are the same state
// because they mean the same thing to the sender — fix it and send again.
const (
	StatusDraft     = "draft"
	StatusSubmitted = "submitted"
	StatusReturned  = "returned"
	StatusApproved  = "approved"
)

// Period is a reporting window every company answers for.
type Period struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
	DueAt       string `json:"due_at"`
	Status      string `json:"status"`
	// MySubmission is this organisation's latest answer, or nil.
	MySubmission *Submission `json:"my_submission,omitempty"`
}

// Submission is one answer to one period.
type Submission struct {
	ID           string  `json:"id"`
	PeriodID     string  `json:"period_id"`
	TenantID     string  `json:"tenant_id,omitempty"`
	TenantName   string  `json:"tenant_name,omitempty"`
	Version      int     `json:"version"`
	Status       string  `json:"status"`
	Source       string  `json:"source"`
	FileName     string  `json:"file_name,omitempty"`
	RowCount     int     `json:"row_count"`
	ErrorCount   int     `json:"error_count"`
	WarningCount int     `json:"warning_count"`
	SubmittedAt  *string `json:"submitted_at"`
	ReviewedAt   *string `json:"reviewed_at"`
	ReviewNote   string  `json:"review_note,omitempty"`
	PeriodStart  string  `json:"period_start,omitempty"`
	PeriodEnd    string  `json:"period_end,omitempty"`
	Hash         string  `json:"hash,omitempty"`
}

// StoredLine is a line as it comes back out, with what the system computed.
type StoredLine struct {
	ID       string `json:"id"`
	SiteKind string `json:"site_kind"`
	SiteID   string `json:"site_id"`
	SiteName string `json:"site_name"`
	ReportLine
	ClosingLiters15C *float64 `json:"closing_liters_15c"`
	VarianceLiters   *float64 `json:"variance_liters"`
	VariancePct      *float64 `json:"variance_pct"`
}

// SubmissionDraft is what a portal or an API client posts.
type SubmissionDraft struct {
	Source         string       `json:"source"`
	FileName       string       `json:"file_name"`
	IdempotencyKey string       `json:"idempotency_key"`
	Lines          []ReportLine `json:"lines"`
}

// contextKey builds the key the site/product maps are held under.
func lineKey(kind, id, product string) string { return kind + "|" + id + "|" + product }

// ---------------------------------------------------------------- periods

// handleListPeriods answers the windows still open, newest first, each with
// this organisation's latest submission attached.
//
// One query per concern rather than one join: the submission list is per
// tenant and the period list is not, and a LEFT JOIN across the row-level
// policy would quietly drop periods nobody has answered yet — which are
// exactly the ones the sender needs to see.
func (m *Module) handleListPeriods(w http.ResponseWriter, r *http.Request) {
	if _, ok := nexus.RequireWorkspace(w, r); !ok {
		return
	}

	limit := 30
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 120 {
		limit = v
	}

	rows, err := m.db.Query(r.Context(), `
		SELECT id::text, kind, period_start::text, period_end::text,
		       due_at::text, status
		  FROM petro_report_periods
		 ORDER BY period_start DESC
		 LIMIT $1`, limit)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the periods")
		return
	}
	defer rows.Close()

	periods := []Period{}
	byID := map[string]int{}
	for rows.Next() {
		var p Period
		if err := rows.Scan(&p.ID, &p.Kind, &p.PeriodStart, &p.PeriodEnd, &p.DueAt, &p.Status); err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not read the periods")
			return
		}
		byID[p.ID] = len(periods)
		periods = append(periods, p)
	}
	if rows.Err() != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the periods")
		return
	}

	// The latest version per period, for this organisation only — the policy
	// on the table does the scoping.
	subRows, err := m.db.Query(r.Context(), `
		SELECT DISTINCT ON (s.period_id)
		       s.id::text, s.period_id::text, s.version, s.status, s.source,`+submissionCountsSQL+`,
		       s.submitted_at::text, s.reviewed_at::text,`+submissionReviewNoteSQL+`
		  FROM petro_report_submissions s
		 ORDER BY s.period_id, s.version DESC`)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the submissions")
		return
	}
	defer subRows.Close()

	for subRows.Next() {
		var s Submission
		if err := subRows.Scan(&s.ID, &s.PeriodID, &s.Version, &s.Status, &s.Source,
			&s.RowCount, &s.ErrorCount, &s.WarningCount, &s.SubmittedAt, &s.ReviewedAt,
			&s.ReviewNote); err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not read the submissions")
			return
		}
		if idx, ok := byID[s.PeriodID]; ok {
			copy := s
			periods[idx].MySubmission = &copy
		}
	}

	nexus.JSON(w, http.StatusOK, map[string]any{"periods": periods})
}

// ---------------------------------------------------------------- prefill

// PrefillLine is one row of the form, already filled in as far as the system
// can fill it.
//
// Opening is yesterday's closing and is not editable in the portal: the one
// number a sender must not be able to choose is the one that makes a month of
// loss disappear a day at a time.
type PrefillLine struct {
	SiteKind       string   `json:"site_kind"`
	SiteID         string   `json:"site_id"`
	SiteName       string   `json:"site_name"`
	ProductCode    string   `json:"product_code"`
	ProductLabel   string   `json:"product_label"`
	Opening        float64  `json:"opening_liters"`
	CapacityLiters float64  `json:"capacity_liters"`
	LastPrice      *float64 `json:"last_price_mnt"`
}

// handlePrefill hands back the shape of the form for one period.
func (m *Module) handlePrefill(w http.ResponseWriter, r *http.Request) {
	if _, ok := nexus.RequireWorkspace(w, r); !ok {
		return
	}
	periodID := chi.URLParam(r, "id")
	if !isUUID(periodID) {
		nexus.Error(w, http.StatusBadRequest, "id буруу хэлбэртэй байна")
		return
	}

	period, err := m.readPeriod(r.Context(), periodID)
	if errors.Is(err, pgx.ErrNoRows) {
		nexus.Error(w, http.StatusNotFound, "ийм тайлангийн үе олдсонгүй")
		return
	}
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the period")
		return
	}

	contexts, order, err := m.loadLineContexts(r.Context(), period.PeriodStart)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not assemble the form")
		return
	}

	labels, err := m.productLabels(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the product dictionary")
		return
	}

	lines := make([]PrefillLine, 0, len(order))
	for _, key := range order {
		c := contexts[key]
		kind, id, product := splitLineKey(key)
		opening := 0.0
		if c.PrevClosing != nil {
			opening = *c.PrevClosing
		}
		lines = append(lines, PrefillLine{
			SiteKind: kind, SiteID: id, SiteName: c.SiteName,
			ProductCode: product, ProductLabel: labels[product],
			Opening: opening, CapacityLiters: c.CapacityLiters, LastPrice: c.PrevPrice,
		})
	}

	nexus.JSON(w, http.StatusOK, map[string]any{"period": period, "lines": lines})
}

// ---------------------------------------------------------------- submit

// handleSubmit takes a whole period's figures, judges them, and stores the
// judgement with them.
//
// One transaction: a submission whose lines were written and whose findings
// were not would read as a clean report.
func (m *Module) handleSubmit(w http.ResponseWriter, r *http.Request) {
	periodID := chi.URLParam(r, "id")

	var draft SubmissionDraft
	// Eleven hundred forecourts times seven grades is the largest report this
	// endpoint should ever see; 8 MB holds it several times over.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&draft); err != nil {
		nexus.Error(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if len(draft.Lines) == 0 {
		nexus.Error(w, http.StatusBadRequest, "тайланд нэг ч мөр алга")
		return
	}
	if draft.Source == "" {
		draft.Source = "form"
	}

	m.submitDraft(w, r, periodID, draft)
}

// submitDraft is the pipeline both doors share.
//
// A spreadsheet and a JSON body must be judged by the same rules and land in
// the same tables, so they meet here rather than each carrying their own copy
// of the validation, the versioning and the chain.
func (m *Module) submitDraft(w http.ResponseWriter, r *http.Request, periodID string, draft SubmissionDraft) {
	claims, err := nexus.UserFromContext(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	tenantID, ok := nexus.RequireWorkspace(w, r)
	if !ok {
		return
	}
	// Both doors reach here — the form and the workbook — so the shape of the
	// period id is checked once, before a 16 MB upload has been parsed.
	if !isUUID(periodID) {
		nexus.Error(w, http.StatusBadRequest, "id буруу хэлбэртэй байна")
		return
	}

	period, err := m.readPeriod(r.Context(), periodID)
	if errors.Is(err, pgx.ErrNoRows) {
		nexus.Error(w, http.StatusNotFound, "ийм тайлангийн үе олдсонгүй")
		return
	}
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the period")
		return
	}
	if period.Status != "open" {
		nexus.Error(w, http.StatusConflict, "энэ тайлангийн үе хаагдсан байна")
		return
	}

	pol := m.LoadPolicy(r.Context())
	contexts, _, err := m.loadLineContexts(r.Context(), period.PeriodStart)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not assemble the register")
		return
	}
	categories, err := m.productCategories(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the product dictionary")
		return
	}

	type judged struct {
		line     ReportLine
		findings []Finding
		std      *float64
		residual float64
		pct      *float64
	}

	graded := make([]judged, 0, len(draft.Lines))
	var allFindings []Finding
	for _, l := range draft.Lines {
		ctx := contexts[lineKey(l.SiteKind, l.SiteID, l.ProductCode)]
		ctx.JODICategory = categories[l.ProductCode]

		findings := ValidateLine(l, ctx, pol)
		allFindings = append(allFindings, findings...)

		j := judged{line: l, findings: findings, residual: BalanceResidual(l)}
		if corrected, ok := CorrectToStandard(ctx.JODICategory, l.Closing, l.TemperatureC, l.DensityKgM3); ok {
			j.std = &corrected
		}
		if throughput := l.Opening + l.Receipts; throughput > 0 {
			// variance_pct is NUMERIC(8,4), so ±9999.9999. A closing figure
			// typed into the wrong box — a totaliser reading where a level
			// belongs — produces tens of thousands of per cent and a numeric
			// overflow that rolls the whole submission back. The number is a
			// signal, not a measurement: clamped, it still says "impossible".
			pct := j.residual / throughput * 100
			if pct > 9999 {
				pct = 9999
			}
			if pct < -9999 {
				pct = -9999
			}
			j.pct = &pct
		}
		graded = append(graded, j)
	}

	errorCount, warningCount := CountBySeverity(allFindings)
	status := StatusSubmitted
	if errorCount > 0 {
		status = StatusReturned
	}

	tx, err := m.db.Begin(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not open a transaction")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()

	// Version and chain position come from what this organisation has already
	// sent. Both inside the transaction, so two submissions racing cannot take
	// the same version — the unique index refuses the loser.
	var version int
	var seq int64
	var prevHash []byte
	// Every one of these three is per company, and none of them said so.
	//
	// The row-level policy on this table is deliberately wider than one
	// organisation — a supervisory body reads across all of them — so a query
	// that leaves the tenant out does not get narrowed by the policy the way an
	// ordinary one would. A sender inside a supervisory body was numbering
	// versions from somebody else's history, colliding on the (tenant_id, seq)
	// index, and chaining their hash to another company's submission (audit
	// §12). tenantID was already in scope; it simply was not used.
	err = tx.QueryRow(r.Context(), `
		SELECT COALESCE(MAX(version) FILTER (WHERE period_id = $2), 0) + 1,
		       COALESCE(MAX(seq), 0) + 1,
		       (SELECT hash FROM petro_report_submissions
		         WHERE tenant_id = $1
		           AND seq = (SELECT MAX(seq) FROM petro_report_submissions WHERE tenant_id = $1))
		  FROM petro_report_submissions
		 WHERE tenant_id = $1`, tenantID, periodID).Scan(&version, &seq, &prevHash)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not place the submission")
		return
	}

	hash := chainHash(prevHash, tenantID, periodID, version, draft.Lines)

	var idempotency *string
	if draft.IdempotencyKey != "" {
		idempotency = &draft.IdempotencyKey
	}

	var submission Submission
	err = tx.QueryRow(r.Context(), `
		INSERT INTO petro_report_submissions
		       (tenant_id, period_id, version, status, source, file_name,
		        row_count, error_count, warning_count, submitted_by, submitted_at,
		        idempotency_key, prev_hash, hash, seq)
		VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10::uuid, NOW(), $11, $12, $13, $14)
		RETURNING id::text, period_id::text, version, status, source, file_name,
		          row_count, error_count, warning_count, submitted_at::text`,
		tenantID, periodID, version, status, draft.Source, draft.FileName,
		len(draft.Lines), errorCount, warningCount, claims.UserID,
		idempotency, prevHash, hash, seq).
		Scan(&submission.ID, &submission.PeriodID, &submission.Version, &submission.Status,
			&submission.Source, &submission.FileName, &submission.RowCount,
			&submission.ErrorCount, &submission.WarningCount, &submission.SubmittedAt)
	if isUniqueViolation(err) {
		nexus.Error(w, http.StatusConflict, "энэ тайлан аль хэдийн ирсэн байна")
		return
	}
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not record the submission")
		return
	}
	submission.Hash = hex.EncodeToString(hash)

	for _, j := range graded {
		// A line the dictionary does not know cannot be stored: product_code is
		// a foreign key, and inserting it would raise 23503 and roll back the
		// other eleven hundred rows — the submission would come back as a bare
		// 400 with no findings, which is the opposite of what this module
		// promises (audit §11). The finding is kept against the submission
		// instead, so the sender still learns which row and why.
		if _, known := categories[j.line.ProductCode]; !known {
			continue
		}
		var lineID string
		err = tx.QueryRow(r.Context(), `
			INSERT INTO petro_report_lines
			       (submission_id, tenant_id, site_kind, site_id, product_code,
			        opening_liters, receipts_liters, sales_liters, transfers_out_liters,
			        adjustments_liters, closing_liters, price_mnt, temperature_c,
			        density_kg_m3, closing_liters_15c, variance_liters, variance_pct, note)
			VALUES ($1::uuid, $2, $3, NULLIF($4, '')::uuid, $5, $6, $7, $8, $9, $10, $11,
			        $12, $13, $14, $15, $16, $17, $18)
			RETURNING id::text`,
			submission.ID, tenantID, j.line.SiteKind, j.line.SiteID, j.line.ProductCode,
			j.line.Opening, j.line.Receipts, j.line.Sales, j.line.TransfersOut,
			j.line.Adjustments, j.line.Closing, j.line.PriceMNT, j.line.TemperatureC,
			j.line.DensityKgM3, j.std, j.residual, j.pct, j.line.Note).Scan(&lineID)
		if err != nil {
			nexus.Error(w, http.StatusBadRequest,
				fmt.Sprintf("мөрийг хадгалж чадсангүй (%s / %s)", j.line.SiteID, j.line.ProductCode))
			return
		}

		if err := m.writeFindings(r.Context(), tx, submission.ID, tenantID, &lineID, j.findings); err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not record the findings")
			return
		}
	}

	// Findings for the lines that could not be stored at all.
	for _, j := range graded {
		if _, known := categories[j.line.ProductCode]; known {
			continue
		}
		if err := m.writeFindings(r.Context(), tx, submission.ID, tenantID, nil, j.findings); err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not record the findings")
			return
		}
	}

	if err := tx.Commit(r.Context()); err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not commit the submission")
		return
	}

	nexus.Audit(r.Context(), tenantID, claims.UserID, "petro.report.submitted", submission.ID,
		map[string]any{
			"period_id": periodID, "version": version, "rows": len(draft.Lines),
			"errors": errorCount, "warnings": warningCount, "status": status,
		})

	nexus.JSON(w, http.StatusCreated, map[string]any{
		"submission": submission,
		"findings":   allFindings,
	})
}

// ---------------------------------------------------------------- reads

// handleListSubmissions answers this organisation's own history.
func (m *Module) handleListSubmissions(w http.ResponseWriter, r *http.Request) {
	if _, ok := nexus.RequireWorkspace(w, r); !ok {
		return
	}

	rows, err := m.db.Query(r.Context(), `
		SELECT s.id::text, s.period_id::text, s.version, s.status, s.source, s.file_name,`+submissionCountsSQL+`,
		       s.submitted_at::text, s.reviewed_at::text,`+submissionReviewNoteSQL+`,
		       p.period_start::text, p.period_end::text
		  FROM petro_report_submissions s
		  JOIN petro_report_periods p ON p.id = s.period_id
		 ORDER BY p.period_start DESC, s.version DESC
		 LIMIT 200`)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the submissions")
		return
	}
	defer rows.Close()

	out := []Submission{}
	for rows.Next() {
		var s Submission
		if err := rows.Scan(&s.ID, &s.PeriodID, &s.Version, &s.Status, &s.Source, &s.FileName,
			&s.RowCount, &s.ErrorCount, &s.WarningCount, &s.SubmittedAt, &s.ReviewedAt,
			&s.ReviewNote, &s.PeriodStart, &s.PeriodEnd); err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not read the submissions")
			return
		}
		out = append(out, s)
	}
	nexus.JSON(w, http.StatusOK, map[string]any{"submissions": out})
}

// handleReadSubmission answers one submission with its lines and findings.
//
// Reachable by the sender and, through the oversight policy, by the regulator.
// Neither needs a different handler: the row-level policy decides who sees the
// row, and a second code path is a second place to forget the check.
func (m *Module) handleReadSubmission(w http.ResponseWriter, r *http.Request) {
	if _, ok := nexus.RequireWorkspace(w, r); !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if !isUUID(id) {
		nexus.Error(w, http.StatusBadRequest, "id буруу хэлбэртэй байна")
		return
	}

	var s Submission
	err := m.db.QueryRow(r.Context(), `
		SELECT s.id::text, s.period_id::text, s.tenant_id::text, t.name, s.version, s.status,
		       s.source, s.file_name,`+submissionCountsSQL+`,
		       s.submitted_at::text, s.reviewed_at::text,`+submissionReviewNoteSQL+`,
		       p.period_start::text, p.period_end::text, encode(s.hash, 'hex')
		  FROM petro_report_submissions s
		  JOIN petro_report_periods p ON p.id = s.period_id
		  JOIN registry.tenants t ON t.id = s.tenant_id
		 WHERE s.id = $1::uuid`, id).
		Scan(&s.ID, &s.PeriodID, &s.TenantID, &s.TenantName, &s.Version, &s.Status,
			&s.Source, &s.FileName, &s.RowCount, &s.ErrorCount, &s.WarningCount,
			&s.SubmittedAt, &s.ReviewedAt, &s.ReviewNote, &s.PeriodStart, &s.PeriodEnd, &s.Hash)
	if errors.Is(err, pgx.ErrNoRows) {
		nexus.Error(w, http.StatusNotFound, "ийм тайлан олдсонгүй")
		return
	}
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the submission")
		return
	}

	lines, err := m.readLines(r.Context(), id)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the lines")
		return
	}

	findingRows, err := m.db.Query(r.Context(), `
		SELECT rule, severity, message, COALESCE(line_id::text, '')
		  FROM petro_validation_findings
		 WHERE submission_id = $1::uuid
		 ORDER BY severity, rule`, id)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the findings")
		return
	}
	defer findingRows.Close()

	type findingOut struct {
		Finding
		LineID string `json:"line_id,omitempty"`
	}
	findings := []findingOut{}
	for findingRows.Next() {
		var f findingOut
		if err := findingRows.Scan(&f.Rule, &f.Severity, &f.Message, &f.LineID); err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not read the findings")
			return
		}
		findings = append(findings, f)
	}

	nexus.JSON(w, http.StatusOK, map[string]any{
		"submission": s, "lines": lines, "findings": findings,
	})
}

func (m *Module) readLines(ctx context.Context, submissionID string) ([]StoredLine, error) {
	rows, err := m.db.Query(ctx, `
		SELECT l.id::text, l.site_kind, COALESCE(l.site_id::text, ''),
		       COALESCE(st.name, dp.name, '—'), l.product_code,
		       l.opening_liters::float8, l.receipts_liters::float8, l.sales_liters::float8,
		       l.transfers_out_liters::float8, l.adjustments_liters::float8,
		       l.closing_liters::float8, l.price_mnt::float8, l.temperature_c::float8,
		       l.density_kg_m3::float8, l.closing_liters_15c::float8,
		       l.variance_liters::float8, l.variance_pct::float8, l.note
		  FROM petro_report_lines l
		  LEFT JOIN petro_stations st ON l.site_kind = 'station' AND st.id = l.site_id
		  LEFT JOIN petro_depots dp ON l.site_kind = 'depot' AND dp.id = l.site_id
		 WHERE l.submission_id = $1::uuid
		 ORDER BY l.site_kind, COALESCE(st.name, dp.name, ''), l.product_code`, submissionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []StoredLine{}
	for rows.Next() {
		var l StoredLine
		if err := rows.Scan(&l.ID, &l.SiteKind, &l.SiteID, &l.SiteName, &l.ProductCode,
			&l.Opening, &l.Receipts, &l.Sales, &l.TransfersOut, &l.Adjustments,
			&l.Closing, &l.PriceMNT, &l.TemperatureC, &l.DensityKgM3,
			&l.ClosingLiters15C, &l.VarianceLiters, &l.VariancePct, &l.Note); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- internals

func (m *Module) readPeriod(ctx context.Context, id string) (Period, error) {
	var p Period
	err := m.db.QueryRow(ctx, `
		SELECT id::text, kind, period_start::text, period_end::text, due_at::text, status
		  FROM petro_report_periods WHERE id = $1::uuid`, id).
		Scan(&p.ID, &p.Kind, &p.PeriodStart, &p.PeriodEnd, &p.DueAt, &p.Status)
	return p, err
}

// loadLineContexts assembles what is known about every site this organisation
// must report for, and the order a form should show them in.
//
// The previous closing comes from the last submission that was not returned:
// a rejected report's figures must not become the next period's opening, or a
// refusal would launder itself into the record.
func (m *Module) loadLineContexts(ctx context.Context, periodStart string) (map[string]LineContext, []string, error) {
	contexts := map[string]LineContext{}
	var order []string

	stationRows, err := m.db.Query(ctx, `
		SELECT s.id::text, s.name, i.fuel_type,
		       i.tank_capacity_liters::float8, i.current_stock_liters::float8, i.price_mnt::float8
		  FROM petro_stations s
		  JOIN petro_station_inventory i ON i.station_id = s.id
		 WHERE s.registry_status <> 'closed'
		 ORDER BY s.name, i.fuel_type`)
	if err != nil {
		return nil, nil, err
	}
	defer stationRows.Close()
	for stationRows.Next() {
		var id, name, product string
		var capacity, stock, price float64
		if err := stationRows.Scan(&id, &name, &product, &capacity, &stock, &price); err != nil {
			return nil, nil, err
		}
		key := lineKey("station", id, product)
		priceCopy := price
		stockCopy := stock
		contexts[key] = LineContext{
			SiteExists: true, SiteName: name, CapacityLiters: capacity, PrevPrice: &priceCopy,
			// The register's level is the opening figure for a site that has
			// never reported. The scan read it and discarded it, so a forecourt
			// added this week arrived on the form with an opening of zero and
			// its first balance was struck against a tank the system already
			// knew held twenty thousand litres (audit §28). A previous report
			// still wins — that is set below and overwrites this.
			PrevClosing: &stockCopy,
		}
		order = append(order, key)
	}
	if stationRows.Err() != nil {
		return nil, nil, stationRows.Err()
	}

	depotRows, err := m.db.Query(ctx, `
		SELECT d.id::text, d.name, t.fuel_type,
		       SUM(t.capacity_liters)::float8, SUM(t.current_liters)::float8
		  FROM petro_depots d
		  JOIN petro_depot_tanks t ON t.depot_id = d.id
		 WHERE d.registry_status <> 'closed'
		 GROUP BY d.id, d.name, t.fuel_type
		 ORDER BY d.name, t.fuel_type`)
	if err != nil {
		return nil, nil, err
	}
	defer depotRows.Close()
	for depotRows.Next() {
		var id, name, product string
		var capacity, stock float64
		if err := depotRows.Scan(&id, &name, &product, &capacity, &stock); err != nil {
			return nil, nil, err
		}
		key := lineKey("depot", id, product)
		contexts[key] = LineContext{SiteExists: true, SiteName: name, CapacityLiters: capacity}
		order = append(order, key)
	}
	if depotRows.Err() != nil {
		return nil, nil, depotRows.Err()
	}

	prevRows, err := m.db.Query(ctx, `
		SELECT DISTINCT ON (l.site_kind, l.site_id, l.product_code)
		       l.site_kind, l.site_id::text, l.product_code,
		       l.closing_liters::float8, l.price_mnt::float8
		  FROM petro_report_lines l
		  JOIN petro_report_submissions s ON s.id = l.submission_id
		  JOIN petro_report_periods p ON p.id = s.period_id
		 WHERE p.period_end < $1::date
		   AND s.status IN ('submitted', 'approved')
		 ORDER BY l.site_kind, l.site_id, l.product_code, p.period_end DESC, s.version DESC`,
		periodStart)
	if err != nil {
		return nil, nil, err
	}
	defer prevRows.Close()
	for prevRows.Next() {
		var kind, id, product string
		var closing float64
		var price *float64
		if err := prevRows.Scan(&kind, &id, &product, &closing, &price); err != nil {
			return nil, nil, err
		}
		key := lineKey(kind, id, product)
		c, known := contexts[key]
		if !known {
			// A site that has been closed since it last reported. It keeps its
			// history but is not offered on the form.
			continue
		}
		closingCopy := closing
		c.PrevClosing = &closingCopy
		if price != nil {
			c.PrevPrice = price
		}
		contexts[key] = c
	}

	return contexts, order, prevRows.Err()
}

func (m *Module) productCategories(ctx context.Context) (map[string]string, error) {
	rows, err := m.db.Query(ctx, `SELECT code, jodi_category FROM petro_products WHERE active`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var code, category string
		if err := rows.Scan(&code, &category); err != nil {
			return nil, err
		}
		out[code] = category
	}
	return out, rows.Err()
}

func (m *Module) productLabels(ctx context.Context) (map[string]string, error) {
	rows, err := m.db.Query(ctx, `SELECT code, label_mn FROM petro_products WHERE active`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var code, label string
		if err := rows.Scan(&code, &label); err != nil {
			return nil, err
		}
		out[code] = label
	}
	return out, rows.Err()
}

func splitLineKey(key string) (kind, id, product string) {
	first := -1
	second := -1
	for i := 0; i < len(key); i++ {
		if key[i] != '|' {
			continue
		}
		if first < 0 {
			first = i
			continue
		}
		second = i
		break
	}
	if first < 0 || second < 0 {
		return "", "", ""
	}
	return key[:first], key[first+1 : second], key[second+1:]
}

// chainHash links one submission to the one before it.
//
// The lines go in as canonical JSON rather than as the request body: two
// clients that spell the same figures differently must produce the same hash,
// or the chain records formatting rather than content.
func chainHash(prev []byte, tenantID, periodID string, version int, lines []ReportLine) []byte {
	h := sha256.New()
	h.Write(prev)
	fmt.Fprintf(h, "%s|%s|%d|", tenantID, periodID, version)
	for _, l := range lines {
		// Every figure the line carries, not only the balance.
		//
		// Price, temperature, density and the note were outside the hash, and
		// the retail price is the number with the most obvious reason to be
		// edited afterwards: it is published in petro.prices. A change to it
		// moved no hash at all, which is precisely the question the chain was
		// built to answer (audit §29).
		fmt.Fprintf(h, "%s|%s|%s|%.3f|%.3f|%.3f|%.3f|%.3f|%.3f|%s|%s|%s|%s;",
			l.SiteKind, l.SiteID, l.ProductCode,
			l.Opening, l.Receipts, l.Sales, l.TransfersOut, l.Adjustments, l.Closing,
			optionalNumber(l.PriceMNT), optionalNumber(l.TemperatureC),
			optionalNumber(l.DensityKgM3), l.Note)
	}
	return h.Sum(nil)
}

// EnsurePeriods creates the reporting windows nobody has created yet.
//
// Called on a schedule rather than on a request: a period that only appears
// when somebody opens the portal is a period the punctual sender never sees.
// Weekends included, deliberately — a daily series with two holes a week is
// not a daily series (Kenya, 2026).
func (m *Module) EnsurePeriods(ctx context.Context, through time.Time, pol Policy) error {
	if pol.Cadence != "daily" {
		return nil
	}
	day := through.AddDate(0, 0, -7)
	for !day.After(through) {
		due := time.Date(day.Year(), day.Month(), day.Day(), pol.DueHour, 0, 0, 0, day.Location()).
			AddDate(0, 0, 1)
		if _, err := m.db.Exec(ctx, `
			INSERT INTO petro_report_periods (kind, period_start, period_end, due_at)
			VALUES ('daily', $1::date, $1::date, $2)
			ON CONFLICT (kind, period_start) DO NOTHING`,
			day.Format("2006-01-02"), due); err != nil {
			return err
		}
		day = day.AddDate(0, 0, 1)
	}
	return nil
}

// writeFindings records what validation found, against a line when there is
// one and against the submission when the line could not be stored.
//
// One place rather than two loops with the same INSERT, because the pair
// differ only in whether line_id is null — and a copy that drifted would leave
// one class of finding silently unwritten.
func (m *Module) writeFindings(ctx context.Context, tx pgx.Tx, submissionID, tenantID string,
	lineID *string, findings []Finding,
) error {
	for _, f := range findings {
		var detail []byte
		if f.Detail != nil {
			detail, _ = json.Marshal(f.Detail)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO petro_validation_findings
			       (submission_id, tenant_id, line_id, rule, severity, message, detail)
			VALUES ($1::uuid, $2, NULLIF($3, '')::uuid, $4, $5, $6, $7)`,
			submissionID, tenantID, derefOrEmpty(lineID), f.Rule, f.Severity, f.Message, detail); err != nil {
			return err
		}
	}
	return nil
}

// optionalNumber renders a figure that may be absent, so that "not measured"
// and "measured as zero" hash differently.
func optionalNumber(value *float64) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%.4f", *value)
}
