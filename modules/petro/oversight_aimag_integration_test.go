package petro

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gerege-systems/open-gerege-nexus/backend/pkg/nexus"
	"github.com/jackc/pgx/v5"
)

// A province's supervisory body watches its own province. Before 00018 every
// read policy asked only whether the caller was a supervisory body at all, so a
// province office read the whole country's forecourts, reports and movements.
func TestAProvinceBodySeesOnlyItsOwnProvince(t *testing.T) {
	pool := openFuelPool(t)
	capital := newCompany(t, pool, "capital")
	gobi := newCompany(t, pool, "gobi")
	ministry := newCompany(t, pool, "nationwide")
	ministry.appoint(t, pool)
	province := newCompany(t, pool, "province")
	blank := newCompany(t, pool, "blank")

	// Spelled differently from the company's entry on purpose: the two names
	// are typed by different people.
	for _, body := range []struct {
		c     *company
		aimag string
	}{{province, " дорноговь "}, {blank, ""}} {
		if _, err := pool.Exec(context.Background(), `
			INSERT INTO petro_oversight_bodies (tenant_id, name, scope, aimag)
			VALUES ($1::uuid, 'Аймгийн хяналт (тест)', 'aimag', $2::text)`,
			body.c.tenantID, body.aimag); err != nil {
			t.Fatalf("appoint a province body: %v", err)
		}
	}

	capitalStation := capital.sells(t, "Нийслэлийн ШТС", 30000, 3190)
	// The Gobi company also trades in the capital, and reports both forecourts
	// on one submission — the capital's line with a gap that raises a finding.
	gobiInCapital := gobi.sells(t, "Говийн компанийн нийслэл ШТС", 30000, 3190)

	lat, lon := 44.89, 110.12
	rec := gobi.call(t, gobi.module.handleCreateStation, http.MethodPost, "/stations",
		StationDraft{Name: "Говийн ШТС", Brand: "test", Aimag: "Дорноговь", Lat: &lat, Lon: &lon}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create station: %d %s", rec.Code, rec.Body.String())
	}
	gobiStation := decode[Station](t, rec).ID
	price, capacity := 3190.0, 30000.0
	rec = gobi.call(t, gobi.module.handleSetStationGrade, http.MethodPut, "/stations/x/grades",
		GradeDraft{FuelType: "ai92", PriceMNT: &price, CapacityLiters: &capacity},
		map[string]string{"id": gobiStation})
	if rec.Code != http.StatusOK {
		t.Fatalf("set grade: %d %s", rec.Code, rec.Body.String())
	}

	period := makePeriod(t, pool, "2026-09-10")
	capitalReport := capital.submit(t, period, reportLine(capitalStation, 0, 20000, 19000, 1000))
	gobiReport := gobi.submit(t, period,
		reportLine(gobiStation, 0, 20000, 19000, 1000),
		reportLine(gobiInCapital, 0, 20000, 19000, 1500))

	stations := []string{capitalStation, gobiStation}
	visible := func(c *company) []string {
		t.Helper()
		rows, err := pool.Query(nexus.WithWorkspaceID(context.Background(), c.tenantID),
			`SELECT id::text FROM petro_stations WHERE id = ANY($1::uuid[]) ORDER BY id`, stations)
		if err != nil {
			t.Fatalf("read stations: %v", err)
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatalf("collect stations: %v", err)
		}
		return ids
	}

	if got := visible(ministry); len(got) != 2 {
		t.Fatalf("the national body sees %v, want both stations", got)
	}
	if got := visible(province); len(got) != 1 || got[0] != gobiStation {
		t.Fatalf("the province body sees %v, want only %s", got, gobiStation)
	}
	if got := visible(blank); len(got) != 0 {
		t.Fatalf("a province body with no province sees %v, want nothing", got)
	}

	rec = province.call(t, province.module.handleReadSubmission, http.MethodGet,
		"/report/submissions/x", nil, map[string]string{"id": capitalReport.Submission.ID})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("the province body read another province's report: %d %s", rec.Code, rec.Body.String())
	}
	rec = province.call(t, province.module.handleReadSubmission, http.MethodGet,
		"/report/submissions/x", nil, map[string]string{"id": gobiReport.Submission.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("the province body could not read its own province's report: %d %s",
			rec.Code, rec.Body.String())
	}

	var lines int
	if err := pool.QueryRow(nexus.WithWorkspaceID(context.Background(), province.tenantID), `
		SELECT COUNT(*)::int FROM petro_report_lines WHERE submission_id = ANY($1::uuid[])`,
		[]string{capitalReport.Submission.ID, gobiReport.Submission.ID}).Scan(&lines); err != nil {
		t.Fatalf("count lines: %v", err)
	}
	if lines != 1 {
		t.Fatalf("the province body reads %d report lines, want 1", lines)
	}

	// A finding names its forecourt, so one about the capital must not reach
	// the Gobi's office through the submission they share.
	findingsAbout := func(c *company, station string) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(nexus.WithWorkspaceID(context.Background(), c.tenantID), `
			SELECT COUNT(*)::int FROM petro_validation_findings
			 WHERE submission_id = $1::uuid AND detail->>'site_id' = $2::text`,
			gobiReport.Submission.ID, station).Scan(&n); err != nil {
			t.Fatalf("count findings: %v", err)
		}
		return n
	}
	if findingsAbout(ministry, gobiInCapital) == 0 {
		t.Fatal("the capital line's gap raised no finding, so the next check proves nothing")
	}
	if n := findingsAbout(province, gobiInCapital); n != 0 {
		t.Fatalf("the province body reads %d findings about a forecourt outside its province", n)
	}

	// Nor through the header: its counts are the rows the body can read, while
	// the ministry still sees the submission as it was filed.
	header := func(c *company) Submission {
		t.Helper()
		rec := c.call(t, c.module.handleReadSubmission, http.MethodGet,
			"/report/submissions/x", nil, map[string]string{"id": gobiReport.Submission.ID})
		if rec.Code != http.StatusOK {
			t.Fatalf("read submission: %d %s", rec.Code, rec.Body.String())
		}
		return decode[struct {
			Submission Submission `json:"submission"`
		}](t, rec).Submission
	}
	var visibleErrors, visibleWarnings int
	if err := pool.QueryRow(nexus.WithWorkspaceID(context.Background(), province.tenantID), `
		SELECT COUNT(*) FILTER (WHERE severity = 'error')::int,
		       COUNT(*) FILTER (WHERE severity = 'warning')::int
		  FROM petro_validation_findings WHERE submission_id = $1::uuid`,
		gobiReport.Submission.ID).Scan(&visibleErrors, &visibleWarnings); err != nil {
		t.Fatalf("count visible findings: %v", err)
	}
	if got := header(province); got.RowCount != 1 || got.ErrorCount != visibleErrors ||
		got.WarningCount != visibleWarnings {
		t.Fatalf("the province body's header reads %d rows, %d errors, %d warnings; want 1, %d, %d",
			got.RowCount, got.ErrorCount, got.WarningCount, visibleErrors, visibleWarnings)
	}
	if got := header(ministry); got.RowCount != 2 || got.ErrorCount != gobiReport.Submission.ErrorCount {
		t.Fatalf("the ministry's header reads %d rows, %d errors; want 2, %d",
			got.RowCount, got.ErrorCount, gobiReport.Submission.ErrorCount)
	}
	// Otherwise the checks below would pass on the stored figures too.
	if gobiReport.Submission.RowCount == 1 || gobiReport.Submission.ErrorCount == visibleErrors {
		t.Fatalf("the stored header (%d rows, %d errors) equals what the province sees (1, %d); the test proves nothing",
			gobiReport.Submission.RowCount, gobiReport.Submission.ErrorCount, visibleErrors)
	}

	// The same header on the two lists that carry it.
	inList := func(rec *httptest.ResponseRecorder, where string) Submission {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", where, rec.Code, rec.Body.String())
		}
		for _, s := range decode[struct {
			Submissions []Submission `json:"submissions"`
		}](t, rec).Submissions {
			if s.ID == gobiReport.Submission.ID {
				return s
			}
		}
		t.Fatalf("%s: the Gobi submission is missing", where)
		return Submission{}
	}
	for where, got := range map[string]Submission{
		"review queue": inList(province.call(t, province.module.handleReviewQueue, http.MethodGet,
			"/oversight/queue?status="+gobiReport.Submission.Status, nil, nil), "review queue"),
		"history": inList(province.call(t, province.module.handleListSubmissions, http.MethodGet,
			"/report/submissions", nil, nil), "history"),
	} {
		if got.RowCount != 1 || got.ErrorCount != visibleErrors || got.WarningCount != visibleWarnings {
			t.Fatalf("%s: the province body reads %d rows, %d errors, %d warnings; want 1, %d, %d",
				where, got.RowCount, got.ErrorCount, got.WarningCount, visibleErrors, visibleWarnings)
		}
	}

	// "My submission" on the period list is the caller's own. Both bodies can
	// read the Gobi company's submission for this period and filed none.
	for _, body := range []*company{province, ministry} {
		rec := body.call(t, body.module.handleListPeriods, http.MethodGet, "/report/periods?limit=120", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("periods: %d %s", rec.Code, rec.Body.String())
		}
		for _, p := range decode[struct {
			Periods []Period `json:"periods"`
		}](t, rec).Periods {
			if p.ID == period && p.MySubmission != nil {
				t.Fatalf("an oversight body's period shows submission %s as its own", p.MySubmission.ID)
			}
		}
	}
	rec = gobi.call(t, gobi.module.handleListPeriods, http.MethodGet, "/report/periods?limit=120", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("periods: %d %s", rec.Code, rec.Body.String())
	}
	found := false
	for _, p := range decode[struct {
		Periods []Period `json:"periods"`
	}](t, rec).Periods {
		if p.ID == period {
			found = p.MySubmission != nil && p.MySubmission.ID == gobiReport.Submission.ID
		}
	}
	if !found {
		t.Fatal("the Gobi company's own period list lost its submission")
	}
}
