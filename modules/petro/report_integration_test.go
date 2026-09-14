package petro

// The regulatory loop, asserted against a real database.
//
// Four of the rules below are not enforced by any Go branch and cannot be seen
// in a unit test: the row-level policy that hides one company's figures from
// another and shows them to the ministry, the unique index that stops a period
// being answered twice at the same version, the four-eyes rule, and the hash
// chain that links each submission to the one before it. Those are exactly the
// rules that stop holding without anybody noticing.
//
//	DATABASE_URL=postgres://... go test ./modules/petro/...

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gerege-systems/open-gerege-nexus/backend/pkg/nexus"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// period opens a reporting window and answers its id.
func makePeriod(t *testing.T, pool *pgxpool.Pool, start string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO petro_report_periods (kind, period_start, period_end, due_at)
		VALUES ('daily', $1::date, $1::date, $1::date + INTERVAL '36 hours')
		RETURNING id::text`, start).Scan(&id)
	if err != nil {
		t.Fatalf("create period: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM petro_report_periods WHERE id = $1::uuid`, id)
	})
	return id
}

// appoint makes this organisation the national supervisory body.
func (c *company) appoint(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	c.appointAs(t, pool, "national")
}

// appointAs makes this organisation a supervisory body of the given scope.
func (c *company) appointAs(t *testing.T, pool *pgxpool.Pool, scope string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO petro_oversight_bodies (tenant_id, name, scope)
		VALUES ($1::uuid, 'Зохицуулагч (тест)', $2::text)`, c.tenantID, scope); err != nil {
		t.Fatalf("appoint oversight body: %v", err)
	}
}

// sells registers a forecourt with one grade, which is what gives it a line on
// the report form.
func (c *company) sells(t *testing.T, name string, capacity, price float64) string {
	t.Helper()
	stationID := c.forecourt(t, name)
	rec := c.call(t, c.module.handleSetStationGrade, http.MethodPut, "/stations/x/grades",
		GradeDraft{FuelType: "ai92", PriceMNT: &price, CapacityLiters: &capacity},
		map[string]string{"id": stationID})
	if rec.Code != http.StatusOK {
		t.Fatalf("set grade: %d %s", rec.Code, rec.Body.String())
	}
	return stationID
}

func reportLine(stationID string, opening, receipts, sales, closing float64) ReportLine {
	temp, density := 12.0, 745.0
	price := 3190.0
	return ReportLine{
		SiteKind: "station", SiteID: stationID, ProductCode: "ai92",
		Opening: opening, Receipts: receipts, Sales: sales, Closing: closing,
		PriceMNT: &price, TemperatureC: &temp, DensityKgM3: &density,
	}
}

type submitResponse struct {
	Submission Submission `json:"submission"`
	Findings   []Finding  `json:"findings"`
}

func (c *company) submit(t *testing.T, periodID string, lines ...ReportLine) submitResponse {
	t.Helper()
	rec := c.call(t, c.module.handleSubmit, http.MethodPost, "/report/periods/x/submissions",
		SubmissionDraft{Lines: lines}, map[string]string{"id": periodID})
	if rec.Code != http.StatusCreated {
		t.Fatalf("submit: %d %s", rec.Code, rec.Body.String())
	}
	return decode[submitResponse](t, rec)
}

func TestABalancedSubmissionIsAccepted(t *testing.T) {
	pool := openFuelPool(t)
	filler := newCompany(t, pool, "balanced")
	station := filler.sells(t, "Тайлант ШТС", 30000, 3190)
	period := makePeriod(t, pool, "2026-09-01")

	answer := filler.submit(t, period, reportLine(station, 0, 20000, 19000, 1000))

	if answer.Submission.Status != StatusSubmitted {
		t.Fatalf("status = %q, want %q (findings: %+v)",
			answer.Submission.Status, StatusSubmitted, answer.Findings)
	}
	if answer.Submission.ErrorCount != 0 {
		t.Fatalf("a balanced report produced %d errors: %+v",
			answer.Submission.ErrorCount, answer.Findings)
	}
	if answer.Submission.Hash == "" {
		t.Fatal("the submission was not placed in the chain")
	}
}

func TestAnUnbalancedSubmissionIsReturnedByTheSystem(t *testing.T) {
	pool := openFuelPool(t)
	filler := newCompany(t, pool, "gap")
	station := filler.sells(t, "Зөрүүтэй ШТС", 30000, 3190)
	period := makePeriod(t, pool, "2026-09-02")

	// 0 + 20000 − 19000 = 1000, but 600 is claimed.
	answer := filler.submit(t, period, reportLine(station, 0, 20000, 19000, 600))

	if answer.Submission.Status != StatusReturned {
		t.Fatalf("status = %q, want %q", answer.Submission.Status, StatusReturned)
	}
	found := false
	for _, f := range answer.Findings {
		if f.Rule == "balance_mismatch" && f.Severity == SeverityError {
			found = true
		}
	}
	if !found {
		t.Fatalf("no balance finding on a 400-litre gap: %+v", answer.Findings)
	}

	// The figures are kept, not discarded: a returned report is evidence.
	var stored int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*)::int FROM petro_report_lines WHERE submission_id = $1::uuid`,
		answer.Submission.ID).Scan(&stored); err != nil {
		t.Fatalf("count lines: %v", err)
	}
	if stored != 1 {
		t.Fatalf("a returned submission kept %d lines, want 1", stored)
	}
}

func TestOnlyASupervisoryBodySeesAnotherCompanysReport(t *testing.T) {
	pool := openFuelPool(t)
	filler := newCompany(t, pool, "watched")
	rival := newCompany(t, pool, "rival")
	ministry := newCompany(t, pool, "ministry")
	ministry.appoint(t, pool)

	station := filler.sells(t, "Хараат ШТС", 30000, 3190)
	period := makePeriod(t, pool, "2026-09-03")
	answer := filler.submit(t, period, reportLine(station, 0, 20000, 19000, 1000))

	rec := rival.call(t, rival.module.handleReadSubmission, http.MethodGet,
		"/report/submissions/x", nil, map[string]string{"id": answer.Submission.ID})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a rival read another company's report: %d %s", rec.Code, rec.Body.String())
	}

	rec = ministry.call(t, ministry.module.handleReadSubmission, http.MethodGet,
		"/report/submissions/x", nil, map[string]string{"id": answer.Submission.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("the ministry could not read the report: %d %s", rec.Code, rec.Body.String())
	}
}

func TestTheSubmitterCannotApproveTheirOwnReport(t *testing.T) {
	pool := openFuelPool(t)
	ministry := newCompany(t, pool, "foureyes")
	ministry.appoint(t, pool)

	// The ministry reporting on its own forecourt is contrived, and it is the
	// cheapest way to put the same user on both ends of the workflow — which is
	// exactly what the rule has to refuse.
	station := ministry.sells(t, "Яамны ШТС", 30000, 3190)
	period := makePeriod(t, pool, "2026-09-04")
	answer := ministry.submit(t, period, reportLine(station, 0, 20000, 19000, 1000))

	rec := ministry.call(t, ministry.module.handleReview(StatusApproved), http.MethodPost,
		"/oversight/submissions/x/approve", Verdict{}, map[string]string{"id": answer.Submission.ID})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("the submitter approved their own report: %d %s", rec.Code, rec.Body.String())
	}

	// A second official in the same body may.
	second := &company{module: ministry.module, tenantID: ministry.tenantID, userID: uuid.NewString()}
	rec = second.call(t, second.module.handleReview(StatusApproved), http.MethodPost,
		"/oversight/submissions/x/approve", Verdict{}, map[string]string{"id": answer.Submission.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("a second official could not approve: %d %s", rec.Code, rec.Body.String())
	}
	if decode[Submission](t, rec).Status != StatusApproved {
		t.Fatal("the approval did not stick")
	}
}

func TestAReturnedReportIsCorrectedByANewVersion(t *testing.T) {
	pool := openFuelPool(t)
	filler := newCompany(t, pool, "versions")
	station := filler.sells(t, "Хоёр хувилбар ШТС", 30000, 3190)
	period := makePeriod(t, pool, "2026-09-05")

	first := filler.submit(t, period, reportLine(station, 0, 20000, 19000, 600))
	second := filler.submit(t, period, reportLine(station, 0, 20000, 19000, 1000))

	if first.Submission.Version != 1 || second.Submission.Version != 2 {
		t.Fatalf("versions = %d, %d; want 1, 2",
			first.Submission.Version, second.Submission.Version)
	}
	if second.Submission.Status != StatusSubmitted {
		t.Fatalf("the corrected version was not accepted: %q", second.Submission.Status)
	}

	// Both versions survive. The first one, with its findings, is the record of
	// what was claimed before the correction.
	var versions int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)::int FROM petro_report_submissions WHERE period_id = $1::uuid`,
		period).Scan(&versions); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	if versions != 2 {
		t.Fatalf("kept %d versions, want 2", versions)
	}

	// And the chain links them.
	var linked bool
	if err := pool.QueryRow(context.Background(), `
		SELECT (SELECT prev_hash FROM petro_report_submissions WHERE id = $2::uuid)
		     = (SELECT hash FROM petro_report_submissions WHERE id = $1::uuid)`,
		first.Submission.ID, second.Submission.ID).Scan(&linked); err != nil {
		t.Fatalf("read the chain: %v", err)
	}
	if !linked {
		t.Fatal("the second submission does not carry the first's hash")
	}
}

func TestTheNationalTableCountsWhoDidNotReport(t *testing.T) {
	pool := openFuelPool(t)
	filler := newCompany(t, pool, "coverage")
	ministry := newCompany(t, pool, "coverage-ministry")
	ministry.appoint(t, pool)

	reporting := filler.sells(t, "Тайлагнасан ШТС", 30000, 3190)
	filler.sells(t, "Чимээгүй ШТС", 30000, 3190)
	period := makePeriod(t, pool, "2026-09-06")
	filler.submit(t, period, reportLine(reporting, 0, 20000, 19000, 1000))

	day, err := time.Parse("2006-01-02", "2026-09-06")
	if err != nil {
		t.Fatalf("parse day: %v", err)
	}
	// The refresh runs outside any workspace, as the scheduled job does.
	if err := (&Module{db: pool}).RefreshDaily(context.Background(), day); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	var total, reported int
	if err := pool.QueryRow(context.Background(), `
		SELECT SUM(sites_total)::int, SUM(sites_reported)::int
		  FROM petro_daily_national WHERE day = $1::date`, "2026-09-06").
		Scan(&total, &reported); err != nil {
		t.Fatalf("read the national table: %v", err)
	}
	if total < 2 || reported < 1 || reported >= total {
		t.Fatalf("coverage = %d of %d; want one of two reporting", reported, total)
	}
}

// The sender and the receiver are two organisations, which is the shape the
// movement exists for. This test used to open and close inside one tenant, and
// so never noticed that the receiver could not see the row it was meant to
// close — or that the sender could close its own consignment with any figure.
func TestAMovementIsOpenedAndClosedWithItsGap(t *testing.T) {
	pool := openFuelPool(t)
	hauler := newCompany(t, pool, "movement")
	receiver := newCompany(t, pool, "movement-receiver")
	depotID, _ := hauler.base(t, 100000)
	stationID := receiver.sells(t, "Хүлээн авагч ШТС", 30000, 3190)

	rec := hauler.call(t, hauler.module.handleOpenMovement, http.MethodPost, "/movements",
		MovementDraft{
			FromKind: "depot", FromID: depotID, ToKind: "station", ToID: stationID,
			ProductCode: "ai92", DeclaredLiters: 20000,
		}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("open movement: %d %s", rec.Code, rec.Body.String())
	}
	opened := decode[Movement](t, rec)
	if opened.NationalRef == "" || opened.Status != "open" {
		t.Fatalf("movement came back as %+v", opened)
	}

	// The sender may not sign for its own delivery.
	rec = hauler.call(t, hauler.module.handleCloseMovement, http.MethodPost,
		"/movements/x/receive", MovementReceipt{ReceivedLiters: 20000},
		map[string]string{"id": opened.ID})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("the sender closed its own movement: %d %s", rec.Code, rec.Body.String())
	}
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM petro_movements WHERE id = $1::uuid`, opened.ID).Scan(&status); err != nil {
		t.Fatalf("read the movement: %v", err)
	}
	if status != "open" {
		t.Fatalf("the movement is %q after the sender's refused close, want open", status)
	}

	// Nor write to it directly: the tenant role holds no UPDATE any more.
	senderCtx := nexus.WithWorkspaceID(context.Background(), hauler.tenantID)
	if _, err := pool.Exec(senderCtx,
		`UPDATE petro_movements SET status = 'closed' WHERE id = $1::uuid`, opened.ID); err == nil {
		t.Fatal("the sender's session could still UPDATE its movement directly")
	}

	rec = receiver.call(t, receiver.module.handleCloseMovement, http.MethodPost,
		"/movements/x/receive", MovementReceipt{ReceivedLiters: 19800},
		map[string]string{"id": opened.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("close movement: %d %s", rec.Code, rec.Body.String())
	}
	closed := decode[Movement](t, rec)
	if closed.Status != "closed" {
		t.Fatalf("status = %q, want closed", closed.Status)
	}
	if closed.VariancePct == nil || *closed.VariancePct < 0.9 || *closed.VariancePct > 1.1 {
		t.Fatalf("variance = %v, want about 1%%", closed.VariancePct)
	}

	// Closing it twice is not a second delivery.
	rec = receiver.call(t, receiver.module.handleCloseMovement, http.MethodPost,
		"/movements/x/receive", MovementReceipt{ReceivedLiters: 19800},
		map[string]string{"id": opened.ID})
	if rec.Code != http.StatusConflict {
		t.Fatalf("a closed movement was closed again: %d", rec.Code)
	}

	// A third company, neither end of it, does not see it at all.
	stranger := newCompany(t, pool, "movement-stranger")
	rec = stranger.call(t, stranger.module.handleCloseMovement, http.MethodPost,
		"/movements/x/receive", MovementReceipt{ReceivedLiters: 1},
		map[string]string{"id": opened.ID})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a stranger reached the movement: %d %s", rec.Code, rec.Body.String())
	}
}

// ── аудитын 2-р багц: хүрээ ба хоёр компанийн хоорондох үйлдэл ─────────────

// petro_oversight_bodies.scope was never read, so an audit office could suspend
// a forecourt, approve a report and dispute a movement. Every body still sees;
// only the national one acts.
func TestOnlyANationalBodyActsOnWhatItSees(t *testing.T) {
	pool := openFuelPool(t)
	filler := newCompany(t, pool, "scoped")
	auditor := newCompany(t, pool, "scoped-audit")
	auditor.appointAs(t, pool, "audit")
	ministry := newCompany(t, pool, "scoped-ministry")
	ministry.appoint(t, pool)

	station := filler.sells(t, "Хүрээний ШТС", 30000, 3190)
	period := makePeriod(t, pool, "2026-09-09")
	answer := filler.submit(t, period, reportLine(station, 0, 20000, 19000, 1000))
	depotID, _ := filler.base(t, 100000)
	rec := filler.call(t, filler.module.handleOpenMovement, http.MethodPost, "/movements",
		MovementDraft{FromKind: "depot", FromID: depotID, ToKind: "station", ToID: station,
			ProductCode: "ai92", DeclaredLiters: 1000}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("open movement: %d %s", rec.Code, rec.Body.String())
	}
	movement := decode[Movement](t, rec)

	// Reading stays open to the audit office.
	rec = auditor.call(t, auditor.module.handleReadSubmission, http.MethodGet,
		"/report/submissions/x", nil, map[string]string{"id": answer.Submission.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("the audit office could not read the report: %d %s", rec.Code, rec.Body.String())
	}

	refused := map[string]*httptest.ResponseRecorder{
		"suspend": auditor.call(t, auditor.module.handleSetSiteStatus, http.MethodPost,
			"/oversight/sites/station/x/status", SiteStatusChange{RegistryStatus: "suspended"},
			map[string]string{"kind": "station", "id": station}),
		"approve": auditor.call(t, auditor.module.handleReview(StatusApproved), http.MethodPost,
			"/oversight/submissions/x/approve", Verdict{},
			map[string]string{"id": answer.Submission.ID}),
		"dispute": auditor.call(t, auditor.module.handleDisputeMovement, http.MethodPost,
			"/oversight/movements/x/dispute", Verdict{Note: "шалгалт"},
			map[string]string{"id": movement.ID}),
		"refresh": auditor.call(t, auditor.module.handleRefreshDaily, http.MethodPost,
			"/oversight/daily/refresh?day=2026-09-09", nil, nil),
	}
	for act, got := range refused {
		if got.Code != http.StatusForbidden {
			t.Errorf("an audit-scope body could %s: %d %s", act, got.Code, got.Body.String())
		}
	}

	var subStatus, registry string
	if err := pool.QueryRow(context.Background(), `
		SELECT (SELECT status FROM petro_report_submissions WHERE id = $1::uuid),
		       (SELECT registry_status FROM petro_stations WHERE id = $2::uuid)`,
		answer.Submission.ID, station).Scan(&subStatus, &registry); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if subStatus != StatusSubmitted || registry != "active" {
		t.Fatalf("refused acts still wrote: submission %q, station %q", subStatus, registry)
	}

	// The ministry disputes, and only the status and the note move.
	rec = ministry.call(t, ministry.module.handleDisputeMovement, http.MethodPost,
		"/oversight/movements/x/dispute", Verdict{Note: "зөрүү"},
		map[string]string{"id": movement.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("the ministry could not dispute: %d %s", rec.Code, rec.Body.String())
	}

	// And no oversight session can rewrite a submission's figures any more.
	ministryCtx := nexus.WithWorkspaceID(context.Background(), ministry.tenantID)
	if _, err := pool.Exec(ministryCtx,
		`UPDATE petro_report_submissions SET row_count = 0 WHERE id = $1::uuid`,
		answer.Submission.ID); err == nil {
		t.Fatal("an oversight session could still UPDATE a submission directly")
	}
}

// A depot company supplying somebody else's forecourt is the ordinary case,
// and the one the chain could not carry: the station could not see the run, and
// the sender receiving on its behalf failed at the tank after its own depot had
// been drawn down. Each row here is seeded under the organisation that really
// owns it — depot and trip to one, station to the other.
func TestADeliveryToAnotherCompanysStationIsReceivedByThatCompany(t *testing.T) {
	pool := openFuelPool(t)
	supplier := newCompany(t, pool, "supplier")
	retailer := newCompany(t, pool, "retailer")

	depotID, tankID := supplier.base(t, 100000)
	shipmentID := supplier.declare(t, 50000)
	supplier.clear(t, shipmentID)
	if rec := supplier.receive(t, depotID, tankID, shipmentID, 50000); rec.Code != http.StatusCreated {
		t.Fatalf("receive into depot: %d %s", rec.Code, rec.Body.String())
	}
	stationID := retailer.sells(t, "Худалдан авагчийн ШТС", 40000, 3190)

	rec := supplier.call(t, supplier.module.handleDispatchFromDepot, http.MethodPost,
		"/depots/x/dispatch",
		DispatchDraft{TankID: tankID, ToStationID: stationID, Liters: 12000, TankerPlate: "1111 УБА"},
		map[string]string{"id": depotID})
	if rec.Code != http.StatusCreated {
		t.Fatalf("dispatch: %d %s", rec.Code, rec.Body.String())
	}
	trip := decode[Dispatch](t, rec)

	// The load carries the batch its importer minted at clearing — the
	// supplier's row, a third party's to the station — and the dispatch took it
	// from the tank the consignment was unloaded into.
	var batchCode string
	if err := pool.QueryRow(context.Background(), `
		SELECT b.batch_code
		  FROM petro_dispatch_trips d
		  JOIN petro_batches b ON b.id = d.batch_id
		 WHERE d.id = $1::uuid AND b.customs_shipment_id = $2::uuid`,
		trip.TripID, shipmentID).Scan(&batchCode); err != nil {
		t.Fatalf("the dispatch did not carry the tank's batch: %v", err)
	}

	// The sender cannot sign for the other company's forecourt.
	rec = supplier.call(t, supplier.module.handleReceiveDelivery, http.MethodPost,
		"/trips/x/receive", ReceiveRequest{Liters: 12000}, map[string]string{"id": trip.TripID})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("the sender received into another company's station: %d %s",
			rec.Code, rec.Body.String())
	}

	rec = retailer.call(t, retailer.module.handleReceiveDelivery, http.MethodPost,
		"/trips/x/receive", ReceiveRequest{Liters: 11950}, map[string]string{"id": trip.TripID})
	if rec.Code != http.StatusCreated {
		t.Fatalf("the station could not receive its delivery: %d %s", rec.Code, rec.Body.String())
	}
	if got := decode[Receipt](t, rec); got.StockAfterLiters != 11950 || got.StationID != stationID ||
		got.BatchCode != batchCode {
		t.Fatalf("receipt came back as %+v, want batch %q", got, batchCode)
	}
	var batchReceived float64
	if err := pool.QueryRow(context.Background(), `
		SELECT received_liters::float8 FROM petro_batches WHERE batch_code = $1 AND tenant_id = $2::uuid`,
		batchCode, supplier.tenantID).Scan(&batchReceived); err != nil {
		t.Fatalf("read the batch: %v", err)
	}
	if batchReceived != 11950 {
		t.Fatalf("the importer's batch shows %v received, want 11950", batchReceived)
	}

	// Once, not twice.
	rec = retailer.call(t, retailer.module.handleReceiveDelivery, http.MethodPost,
		"/trips/x/receive", ReceiveRequest{Liters: 11950}, map[string]string{"id": trip.TripID})
	if rec.Code != http.StatusConflict {
		t.Fatalf("a delivery was received twice: %d %s", rec.Code, rec.Body.String())
	}

	var receiptTenant, tripStatus string
	var stock float64
	if err := pool.QueryRow(context.Background(), `
		SELECT (SELECT tenant_id::text FROM petro_station_receipts WHERE trip_id = $1::uuid),
		       (SELECT status FROM petro_dispatch_trips WHERE id = $1::uuid),
		       (SELECT current_stock_liters::float8 FROM petro_station_inventory
		         WHERE station_id = $2::uuid AND fuel_type = 'ai92')`,
		trip.TripID, stationID).Scan(&receiptTenant, &tripStatus, &stock); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if receiptTenant != retailer.tenantID || tripStatus != "completed" || stock != 11950 {
		t.Fatalf("receipt tenant %s (want the station's %s), trip %q, stock %v",
			receiptTenant, retailer.tenantID, tripStatus, stock)
	}

	// A suspended forecourt is not a destination: nothing leaves the tank.
	if _, err := pool.Exec(context.Background(),
		`UPDATE petro_stations SET registry_status = 'suspended' WHERE id = $1::uuid`, stationID); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	before := supplier.tankLevel(t, pool, tankID)
	rec = supplier.call(t, supplier.module.handleDispatchFromDepot, http.MethodPost,
		"/depots/x/dispatch",
		DispatchDraft{TankID: tankID, ToStationID: stationID, Liters: 1000, TankerPlate: "2222 УБА"},
		map[string]string{"id": depotID})
	if rec.Code != http.StatusConflict {
		t.Fatalf("a load was sent to a suspended station: %d %s", rec.Code, rec.Body.String())
	}
	if after := supplier.tankLevel(t, pool, tankID); after != before {
		t.Fatalf("the refused load moved the tank from %v to %v", before, after)
	}

	// Nor is a station that does not exist.
	rec = supplier.call(t, supplier.module.handleDispatchFromDepot, http.MethodPost,
		"/depots/x/dispatch",
		DispatchDraft{TankID: tankID, ToStationID: uuid.NewString(), Liters: 1000, TankerPlate: "3333 УБА"},
		map[string]string{"id": depotID})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a load was sent to no station: %d %s", rec.Code, rec.Body.String())
	}
}

// ── аудитын §4 ба §5-ын засварыг барих тестүүд ───────────────────────────────

// The policy this replaced was FOR UPDATE and knew nothing about columns, so a
// manager inside a supervisory body could rewrite any company's register: its
// name, its coordinates, its licence number. Only the status may cross the
// ownership line, and only through the named action.
func TestAnOversightBodyCannotRewriteAnotherCompanysRegister(t *testing.T) {
	pool := openFuelPool(t)
	owner := newCompany(t, pool, "owned")
	ministry := newCompany(t, pool, "rewriter")
	ministry.appoint(t, pool)

	stationID := owner.sells(t, "Хамгаалагдсан ШТС", 30000, 3190)

	lat, lon := 1.0, 1.0
	rec := ministry.call(t, ministry.module.handleUpdateStation, http.MethodPatch,
		"/stations/x", StationDraft{Name: "Булаасан нэр", Brand: "x", Lat: &lat, Lon: &lon},
		map[string]string{"id": stationID})
	if rec.Code == http.StatusOK {
		t.Fatalf("a supervisory body rewrote another company's station: %s", rec.Body.String())
	}

	var name string
	if err := pool.QueryRow(context.Background(),
		`SELECT name FROM petro_stations WHERE id = $1::uuid`, stationID).Scan(&name); err != nil {
		t.Fatalf("read the station: %v", err)
	}
	if name != "Хамгаалагдсан ШТС" {
		t.Fatalf("the station's name is now %q", name)
	}

	// Suspending it, though, is exactly what a supervisory body is for.
	rec = ministry.call(t, ministry.module.handleSetSiteStatus, http.MethodPost,
		"/oversight/sites/station/x/status",
		SiteStatusChange{RegistryStatus: "suspended", Note: "шалгалт"},
		map[string]string{"kind": "station", "id": stationID})
	if rec.Code != http.StatusOK {
		t.Fatalf("suspend: %d %s", rec.Code, rec.Body.String())
	}
}

// An ordinary company must not be able to suspend anything at all.
func TestOnlyAnOversightBodyCanSuspendASite(t *testing.T) {
	pool := openFuelPool(t)
	owner := newCompany(t, pool, "self-suspend")
	stationID := owner.sells(t, "Өөрийн ШТС", 30000, 3190)

	rec := owner.call(t, owner.module.handleSetSiteStatus, http.MethodPost,
		"/oversight/sites/station/x/status",
		SiteStatusChange{RegistryStatus: "closed"},
		map[string]string{"kind": "station", "id": stationID})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a company suspended a site itself: %d %s", rec.Code, rec.Body.String())
	}
}

// The refresh button answered 42501 on every press: the handler runs inside the
// workspace gate, and the tenant role held SELECT on the national table and
// nothing more. The integration test called RefreshDaily on the raw pool, so
// the HTTP path was never exercised — this one goes through the handler.
func TestTheRefreshButtonWorksFromInsideTheWorkspaceGate(t *testing.T) {
	pool := openFuelPool(t)
	ministry := newCompany(t, pool, "refresher")
	ministry.appoint(t, pool)

	rec := ministry.call(t, ministry.module.handleRefreshDaily, http.MethodPost,
		"/oversight/daily/refresh?day=2026-09-07", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh from the tenant role: %d %s", rec.Code, rec.Body.String())
	}
}

// A day recomputed after its last site closed must lose the row, not keep the
// stock it held — a pure upsert left ghost litres in the national total.
func TestRefreshRetiresTheRowsOfAClosedSite(t *testing.T) {
	pool := openFuelPool(t)
	filler := newCompany(t, pool, "ghost")
	ministry := newCompany(t, pool, "ghost-ministry")
	ministry.appoint(t, pool)

	station := filler.sells(t, "Хаагдах ШТС", 30000, 3190)
	period := makePeriod(t, pool, "2026-09-08")
	filler.submit(t, period, reportLine(station, 0, 20000, 19000, 1000))

	day, _ := time.Parse("2006-01-02", "2026-09-08")
	module := &Module{db: pool}
	if err := module.RefreshDaily(context.Background(), day); err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	var before float64
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(stock_liters), 0)::float8 FROM petro_daily_national WHERE day = $1::date`,
		"2026-09-08").Scan(&before); err != nil {
		t.Fatalf("read the day: %v", err)
	}
	if before < 1000 {
		t.Fatalf("the first refresh recorded %v litres, want the reported 1000", before)
	}

	if _, err := pool.Exec(context.Background(),
		`UPDATE petro_stations SET registry_status = 'closed' WHERE id = $1::uuid`, station); err != nil {
		t.Fatalf("close the station: %v", err)
	}
	if err := module.RefreshDaily(context.Background(), day); err != nil {
		t.Fatalf("second refresh: %v", err)
	}

	var after float64
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(stock_liters), 0)::float8 FROM petro_daily_national WHERE day = $1::date`,
		"2026-09-08").Scan(&after); err != nil {
		t.Fatalf("read the day again: %v", err)
	}
	if after != 0 {
		t.Fatalf("a closed site left %v litres in the national total", after)
	}
}

// ── аудитын §1 ба §2 — гинжний хасах тэмдэг ──────────────────────────────────

// The bug this guards: every stock write in the module was an addition, so a
// litre that left a depot was still counted there and counted again where it
// arrived. The national total — the number the system exists to produce — was
// double.
func TestFuelLeavesTheDepotWhenALorryIsLoaded(t *testing.T) {
	pool := openFuelPool(t)
	company := newCompany(t, pool, "outflow")
	depotID, tankID := company.base(t, 100000)
	shipmentID := company.declare(t, 60000)
	company.clear(t, shipmentID)
	if rec := company.receive(t, depotID, tankID, shipmentID, 60000); rec.Code != http.StatusCreated {
		t.Fatalf("receive into depot: %d %s", rec.Code, rec.Body.String())
	}
	if level := company.tankLevel(t, pool, tankID); level != 60000 {
		t.Fatalf("tank holds %v after the receipt, want 60000", level)
	}

	stationID := company.sells(t, "Хүлээн авагч ШТС", 40000, 3190)
	rec := company.call(t, company.module.handleDispatchFromDepot, http.MethodPost,
		"/depots/x/dispatch",
		DispatchDraft{TankID: tankID, ToStationID: stationID, Liters: 20000,
			TankerPlate: "1234 УБА", OpenMovement: true},
		map[string]string{"id": depotID})
	if rec.Code != http.StatusCreated {
		t.Fatalf("dispatch: %d %s", rec.Code, rec.Body.String())
	}
	out := decode[Dispatch](t, rec)
	if out.TankAfterLiters != 40000 {
		t.Fatalf("tank holds %v after loading 20000 of 60000, want 40000", out.TankAfterLiters)
	}
	if level := company.tankLevel(t, pool, tankID); level != 40000 {
		t.Fatalf("the tank row says %v, want 40000", level)
	}
	if out.MovementRef == "" {
		t.Fatal("the load opened no movement")
	}
}

// A base cannot send out more than it holds, and the refusal is the database's.
func TestADepotCannotDispatchMoreThanItHolds(t *testing.T) {
	pool := openFuelPool(t)
	company := newCompany(t, pool, "overdraw")
	depotID, tankID := company.base(t, 100000)
	shipmentID := company.declare(t, 10000)
	company.clear(t, shipmentID)
	company.receive(t, depotID, tankID, shipmentID, 10000)

	rec := company.call(t, company.module.handleDispatchFromDepot, http.MethodPost,
		"/depots/x/dispatch",
		DispatchDraft{TankID: tankID, Liters: 25000, TankerPlate: "5678 УБА"},
		map[string]string{"id": depotID})
	if rec.Code != http.StatusConflict {
		t.Fatalf("an overdraw answered %d %s", rec.Code, rec.Body.String())
	}
	if level := company.tankLevel(t, pool, tankID); level != 10000 {
		t.Fatalf("the refused load still moved the tank to %v", level)
	}
}

// Audit §2: the voucher columns existed, the reader read them, and nothing
// ever wrote them — so a voucher's only ending was expiry, which gives the
// money back. Selling fuel is what closes it, and it closes exactly once.
func TestASaleDrawsDownTheForecourtAndClosesTheVoucher(t *testing.T) {
	pool := openFuelPool(t)
	company := newCompany(t, pool, "sale")
	stationID := company.sells(t, "Түгээх ШТС", 40000, 3190)

	// Fuel has to arrive before it can be sold.
	if _, err := pool.Exec(context.Background(), `
		UPDATE petro_station_inventory SET current_stock_liters = 5000
		 WHERE station_id = $1::uuid AND fuel_type = 'ai92'`, stationID); err != nil {
		t.Fatalf("seed the tank: %v", err)
	}

	// The voucher belongs to the citizens' organisation, not to the operator.
	//
	// That is the only shape production ever has — eID just-in-time
	// provisioning puts every citizen in one organisation of its own — and the
	// shape this test used to get wrong: it minted the voucher against
	// `company.tenantID`, so the redemption happened inside one tenant and the
	// row-level policy was never asked. In production it was asked, answered
	// with zero rows, and every scan came back "already spent".
	citizens := newCompany(t, pool, "citizens")
	voucherID, qr := issueVoucher(t, pool, citizens.tenantID)

	// The id alone is not enough: it is written to the audit log and is no
	// secret. Refused before any fuel moves.
	rec := company.call(t, company.module.handleRecordSale, http.MethodPost, "/stations/x/sales",
		SaleDraft{FuelType: "ai92", Liters: 15, VoucherID: voucherID},
		map[string]string{"id": stationID})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a voucher was spent by its id alone: %d %s", rec.Code, rec.Body.String())
	}
	// And the function refuses it too, for any caller that skips the handler.
	operatorCtx := nexus.WithWorkspaceID(context.Background(), company.tenantID)
	if err := pool.QueryRow(operatorCtx,
		`SELECT voucher_id FROM petro_redeem_voucher($1::uuid, NULL, $2::uuid, 15)`,
		voucherID, stationID).Scan(new(string)); !isCheckViolation(err) {
		t.Fatalf("petro_redeem_voucher took an id without a code: %v", err)
	}

	rec = company.call(t, company.module.handleRecordSale, http.MethodPost, "/stations/x/sales",
		SaleDraft{FuelType: "ai92", Liters: 15, VoucherID: voucherID, QRToken: qr},
		map[string]string{"id": stationID})
	if rec.Code != http.StatusCreated {
		t.Fatalf("sale: %d %s", rec.Code, rec.Body.String())
	}
	if sale := decode[Sale](t, rec); sale.StockAfterLiter != 4985 {
		t.Fatalf("stock is %v after selling 15 of 5000, want 4985", sale.StockAfterLiter)
	}

	var status string
	var redeemed *string
	if err := pool.QueryRow(context.Background(),
		`SELECT status, redeemed_at::text FROM petro_vouchers WHERE id = $1::uuid`,
		voucherID).Scan(&status, &redeemed); err != nil {
		t.Fatalf("read the voucher: %v", err)
	}
	if status != "redeemed" || redeemed == nil {
		t.Fatalf("the voucher is %q, redeemed_at %v", status, redeemed)
	}

	// A second scan of the same code must not dispense again.
	rec = company.call(t, company.module.handleRecordSale, http.MethodPost, "/stations/x/sales",
		SaleDraft{FuelType: "ai92", Liters: 15, QRToken: qr},
		map[string]string{"id": stationID})
	if rec.Code != http.StatusConflict {
		t.Fatalf("a spent voucher was accepted again: %d %s", rec.Code, rec.Body.String())
	}
}

// issueVoucher mints an active voucher held by the given organisation.
//
// Straight into the table on the login role rather than through the citizen's
// endpoint: what is being tested is who may close it, and the issuing side has
// its own tenant, its own session and nothing to do with the forecourt.
func issueVoucher(t *testing.T, pool *pgxpool.Pool, tenantID string) (id, qr string) {
	t.Helper()
	qr = uuid.NewString()
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO petro_vouchers
		       (citizen_id, tenant_id, for_date, amount_mnt, fuel_type, qr_token, expires_at)
		VALUES (gen_random_uuid(), $1::uuid, CURRENT_DATE, 50000, 'ai92', $2, NOW() + INTERVAL '3 hours')
		RETURNING id::text`, tenantID, qr).Scan(&id); err != nil {
		t.Fatalf("create a voucher: %v", err)
	}
	return id, qr
}

// A voucher is spendable at any pump, so the function that closes it cannot ask
// who owns the voucher. What it must ask is whether the forecourt handing over
// the fuel belongs to the caller — otherwise one operator could close vouchers
// against a competitor's site and leave the litres on their books.
func TestOnlyTheOperatorOfTheForecourtCanCloseAVoucher(t *testing.T) {
	pool := openFuelPool(t)
	seller := newCompany(t, pool, "seller")
	stationID := seller.sells(t, "Ваучер авдаг ШТС", 40000, 3190)
	if _, err := pool.Exec(context.Background(), `
		UPDATE petro_station_inventory SET current_stock_liters = 5000
		 WHERE station_id = $1::uuid AND fuel_type = 'ai92'`, stationID); err != nil {
		t.Fatalf("seed the tank: %v", err)
	}

	citizens := newCompany(t, pool, "citizens2")
	voucherID, qr := issueVoucher(t, pool, citizens.tenantID)

	stranger := newCompany(t, pool, "stranger")
	rec := stranger.call(t, stranger.module.handleRecordSale, http.MethodPost, "/stations/x/sales",
		SaleDraft{FuelType: "ai92", Liters: 15, VoucherID: voucherID, QRToken: qr},
		map[string]string{"id": stationID})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("another operator closed a voucher on a site that is not theirs: %d %s",
			rec.Code, rec.Body.String())
	}

	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM petro_vouchers WHERE id = $1::uuid`, voucherID).Scan(&status); err != nil {
		t.Fatalf("read the voucher: %v", err)
	}
	if status != "active" {
		t.Fatalf("the voucher is %q after a refused sale, want active", status)
	}
}

// Audit §8: the forecourt table had no capacity CHECK, so a delivery into a
// nearly-full tank produced a level no vessel could hold — invisible on screen
// because the public API clamps the percentage at 100.
func TestAForecourtTankCannotBeOverfilled(t *testing.T) {
	pool := openFuelPool(t)
	company := newCompany(t, pool, "overfill")
	stationID := company.sells(t, "Дүүрэн ШТС", 30000, 3190)

	if _, err := pool.Exec(context.Background(), `
		UPDATE petro_station_inventory SET current_stock_liters = 28000
		 WHERE station_id = $1::uuid AND fuel_type = 'ai92'`, stationID); err != nil {
		t.Fatalf("seed the tank: %v", err)
	}
	_, err := pool.Exec(context.Background(), `
		UPDATE petro_station_inventory SET current_stock_liters = current_stock_liters + 20000
		 WHERE station_id = $1::uuid AND fuel_type = 'ai92'`, stationID)
	if err == nil {
		t.Fatal("a 30,000 litre tank accepted 48,000 litres")
	}
}
