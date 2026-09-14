package petro

// The station register, asserted against a real database.
//
// What is worth testing here is not that an INSERT inserts. It is the three
// rules that live outside the Go code and would stop holding silently:
//
//   - a forecourt with history cannot be deleted, so the chain a batch
//     travelled cannot be removed by tidying up a station list
//   - another company's station is not there to be edited, which is the
//     row-level policy rather than a WHERE clause in the handler
//   - a grade is keyed by (station, fuel type), so registering АИ-92 twice
//     states the same fact twice rather than creating two pumps
//
//	FUEL_TEST_DATABASE_URL=postgres://... go test ./internal/apps/petro/...

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// forecourt registers a station and answers its id.
func (c *company) forecourt(t *testing.T, name string) string {
	t.Helper()
	lat, lon := 47.92, 106.92
	rec := c.call(t, c.module.handleCreateStation, http.MethodPost, "/stations",
		StationDraft{Name: name, Brand: "test", Aimag: "Улаанбаатар", Lat: &lat, Lon: &lon}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create station: %d %s", rec.Code, rec.Body.String())
	}
	return decode[Station](t, rec).ID
}

func TestAStationIsRegisteredWithItsGrades(t *testing.T) {
	pool := openFuelPool(t)
	company := newCompany(t, pool, "grades")

	stationID := company.forecourt(t, "Тест ШТС-1")

	price, capacity := 2450.0, 30000.0
	rec := company.call(t, company.module.handleSetStationGrade, http.MethodPut, "/stations/x/grades",
		GradeDraft{FuelType: "ai92", PriceMNT: &price, CapacityLiters: &capacity},
		map[string]string{"id": stationID})
	if rec.Code != http.StatusOK {
		t.Fatalf("set grade: %d %s", rec.Code, rec.Body.String())
	}
	if grade := decode[StationGrade](t, rec); grade.PriceMNT != price || grade.FuelLabel != "ai92" {
		t.Errorf("grade came back as %+v", grade)
	}

	// The same grade again is the same row: a forecourt does not grow a second
	// АИ-92 pump because somebody corrected the price.
	newPrice := 2500.0
	rec = company.call(t, company.module.handleSetStationGrade, http.MethodPut, "/stations/x/grades",
		GradeDraft{FuelType: "ai92", PriceMNT: &newPrice}, map[string]string{"id": stationID})
	if rec.Code != http.StatusOK {
		t.Fatalf("re-set grade: %d %s", rec.Code, rec.Body.String())
	}

	rec = company.call(t, company.module.handleListStations, http.MethodGet, "/stations", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	listed := decode[struct {
		Stations []Station `json:"stations"`
	}](t, rec).Stations
	if len(listed) != 1 {
		t.Fatalf("expected one station, got %d", len(listed))
	}
	if got := listed[0].Fuels; len(got) != 1 || got[0].PriceMNT != newPrice {
		t.Fatalf("the station's grades came back as %+v", got)
	}
	// The capacity survived a patch that did not mention it, and the litres
	// stayed where receipts left them — there is no route that sets either.
	if listed[0].Fuels[0].CapacityLiters != capacity || listed[0].Fuels[0].CurrentLiters != 0 {
		t.Errorf("capacity/stock came back as %v/%v",
			listed[0].Fuels[0].CapacityLiters, listed[0].Fuels[0].CurrentLiters)
	}
}

func TestAStationWithHistoryIsNotDeleted(t *testing.T) {
	pool := openFuelPool(t)
	company := newCompany(t, pool, "history")

	fresh := company.forecourt(t, "Устгагдах ШТС")
	rec := company.call(t, company.module.handleDeleteStation, http.MethodDelete, "/stations/x",
		nil, map[string]string{"id": fresh})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("a station nothing has happened at should be deletable: %d %s", rec.Code, rec.Body.String())
	}

	// One with a delivery behind it is a record, not a typo.
	used := company.forecourt(t, "Ачаа хүлээж авсан ШТС")
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO petro_station_receipts (tenant_id, station_id, fuel_type, liters)
		VALUES ($1, $2, 'ai92', 12000)`, company.tenantID, used); err != nil {
		t.Fatalf("record a receipt: %v", err)
	}
	rec = company.call(t, company.module.handleDeleteStation, http.MethodDelete, "/stations/x",
		nil, map[string]string{"id": used})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a station with receipts, got %d %s", rec.Code, rec.Body.String())
	}

	var still int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*)::int FROM petro_stations WHERE id = $1`, used).Scan(&still); err != nil {
		t.Fatal(err)
	}
	if still != 1 {
		t.Error("the station was deleted anyway, and its receipts went with it")
	}
}

func TestAnotherCompanysStationCannotBeEdited(t *testing.T) {
	pool := openFuelPool(t)
	mine := newCompany(t, pool, "mine")
	theirs := newCompany(t, pool, "theirs")

	stationID := mine.forecourt(t, "Миний ШТС")

	renamed := "Хулгайлсан нэр"
	rec := theirs.call(t, theirs.module.handleUpdateStation, http.MethodPatch, "/stations/x",
		StationPatch{Name: &renamed}, map[string]string{"id": stationID})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 across organisations, got %d %s", rec.Code, rec.Body.String())
	}

	rec = theirs.call(t, theirs.module.handleSetStationGrade, http.MethodPut, "/stations/x/grades",
		GradeDraft{FuelType: "ai95"}, map[string]string{"id": stationID})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 setting a grade across organisations, got %d %s", rec.Code, rec.Body.String())
	}

	var name string
	if err := pool.QueryRow(context.Background(),
		`SELECT name FROM petro_stations WHERE id = $1`, stationID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Миний ШТС" {
		t.Errorf("the station was renamed by another organisation: %q", name)
	}
}

// A station has to be somewhere. lat/lon are NOT NULL in the schema, so a
// handler that let them through would answer 500 to a form that is merely
// incomplete — and (0, 0) is a real place in the Gulf of Guinea, which reads on
// the map as a bug rather than as a missing field.
func TestAStationWithoutCoordinatesIsRefused(t *testing.T) {
	pool := openFuelPool(t)
	company := newCompany(t, pool, "nowhere")

	rec := company.call(t, company.module.handleCreateStation, http.MethodPost, "/stations",
		StationDraft{Name: "Хаана ч биш " + uuid.NewString()[:6]}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without coordinates, got %d %s", rec.Code, rec.Body.String())
	}

	zero := 0.0
	rec = company.call(t, company.module.handleCreateStation, http.MethodPost, "/stations",
		StationDraft{Name: "Тэг цэг", Lat: &zero, Lon: &zero}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 at (0, 0), got %d %s", rec.Code, rec.Body.String())
	}
}

// A path parameter that is not a uuid is the caller's mistake, not the
// server's.
//
// Half the handlers checked the shape and half passed the string to a `$1::uuid`
// cast, where Postgres answered 22P02 and the module turned that into a 500 —
// the status that means "something here is broken", logged as if it were.
func TestAMalformedIdIsRefused(t *testing.T) {
	pool := openFuelPool(t)
	company := newCompany(t, pool, "malformed")

	for _, probe := range []struct {
		name    string
		handler http.HandlerFunc
		method  string
		params  map[string]string
	}{
		{"update a station", company.module.handleUpdateStation, http.MethodPatch,
			map[string]string{"id": "not-a-uuid"}},
		{"delete a station", company.module.handleDeleteStation, http.MethodDelete,
			map[string]string{"id": "not-a-uuid"}},
		{"set a grade", company.module.handleSetStationGrade, http.MethodPut,
			map[string]string{"id": "not-a-uuid"}},
		{"read a submission", company.module.handleReadSubmission, http.MethodGet,
			map[string]string{"id": "not-a-uuid"}},
		{"a station's receipts", company.module.handleListReceipts, http.MethodGet,
			map[string]string{"id": "not-a-uuid"}},
	} {
		body := any(nil)
		if probe.method == http.MethodPut {
			body = GradeDraft{FuelType: "ai92"}
		}
		rec := company.call(t, probe.handler, probe.method, "/x", body, probe.params)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", probe.name, rec.Code, rec.Body.String())
		}
	}
}

// A province is chosen from the register's list. «Дорноговь аймаг» is the same
// place as «Дорноговь» to a person and a different key to the province body
// that reads by it, so the form's spelling is the only one stored.
func TestAProvinceOffTheListIsRefused(t *testing.T) {
	pool := openFuelPool(t)
	company := newCompany(t, pool, "offlist")
	lat, lon := 44.89, 110.12

	rec := company.call(t, company.module.handleCreateStation, http.MethodPost, "/stations",
		StationDraft{Name: "Жагсаалтгүй ШТС", Brand: "test", Aimag: "Дорноговь аймаг", Lat: &lat, Lon: &lon}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a station off the list: %d %s, want 400", rec.Code, rec.Body.String())
	}

	rec = company.call(t, company.module.handleCreateDepot, http.MethodPost, "/depots",
		DepotDraft{Name: "Жагсаалтгүй бааз", Aimag: "Gobi"}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a depot off the list: %d %s, want 400", rec.Code, rec.Body.String())
	}

	rec = company.call(t, company.module.handleCreateStation, http.MethodPost, "/stations",
		StationDraft{Name: "Жагсаалттай ШТС", Brand: "test", Aimag: " Дорноговь ", Lat: &lat, Lon: &lon}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a listed station: %d %s", rec.Code, rec.Body.String())
	}
	station := decode[Station](t, rec)
	if station.Aimag != "Дорноговь" {
		t.Fatalf("stored aimag %q, want the trimmed list name", station.Aimag)
	}

	bad := "Дорноговь аймаг"
	rec = company.call(t, company.module.handleUpdateStation, http.MethodPatch, "/stations/x",
		StationPatch{Aimag: &bad}, map[string]string{"id": station.ID})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("an update off the list: %d %s, want 400", rec.Code, rec.Body.String())
	}
}
