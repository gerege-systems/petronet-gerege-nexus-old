/*
 * Gerege Nexus
 * Copyright (c) 2026 Gerege Systems Development Team, Gerege Nomadica Foundation
 * Distributed under the Apache 2.0 License.
 *
 * The register's three additions: a national number, a technical census, and
 * the list of who supervises.
 *
 * # The national number
 *
 * A forecourt already has an id in this database and a name on its sign, and
 * neither can be quoted in a licence. The national code is the one identifier
 * that survives a company being sold, a brand changing and a database being
 * migrated — so it is unique across the country rather than within a company,
 * and the index that enforces that lives in migration 00009 rather than here.
 *
 * # The census
 *
 * Before anybody writes a pump driver, somebody has to know what is on the
 * forecourts: which brand, which protocol, whether there is a tank gauge,
 * whether there is internet. Four columns and a class letter answer the single
 * largest unknown in the IoT phase, and they are filled in by the operators
 * themselves through the portal — which is why the census is a PATCH on the
 * station and not a survey somewhere else. A survey nobody links to the
 * register is a survey that goes stale the first time a pump is replaced.
 *
 * # Who supervises
 *
 * petro_oversight_bodies decides who can see across companies, so writing to it
 * is not something a tenant may do to itself. It is reachable only from the
 * deployment console, over the same session check overview.go makes.
 */

package petro

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gerege-systems/open-gerege-nexus/backend/pkg/nexus"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// RegistryPatch carries the identifiers the register keeps for a site.
type RegistryPatch struct {
	NationalCode *string `json:"national_code"`
}

// handleSetNationalCode records the licence number a site is known by.
func (m *Module) handleSetNationalCode(w http.ResponseWriter, r *http.Request) {
	claims, err := nexus.UserFromContext(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	tenantID, ok := nexus.RequireWorkspace(w, r)
	if !ok {
		return
	}
	kind := chi.URLParam(r, "kind")
	id := chi.URLParam(r, "id")
	if !isUUID(id) {
		nexus.Error(w, http.StatusBadRequest, "id буруу хэлбэртэй байна")
		return
	}

	table := ""
	switch kind {
	case "station":
		table = "petro_stations"
	case "depot":
		table = "petro_depots"
	default:
		nexus.Error(w, http.StatusBadRequest, "объектын төрөл нь station эсвэл depot байна")
		return
	}

	var patch RegistryPatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&patch); err != nil {
		nexus.Error(w, http.StatusBadRequest, "invalid payload")
		return
	}

	// An absent field is not an instruction to clear one. `PATCH {}` used to
	// answer 200 with national_code null, and because the index is unique
	// across the country another company's station could then take the freed
	// licence number — with no way back (audit §22). Clearing it is still
	// possible, by saying so: {"national_code": ""}.
	if patch.NationalCode == nil {
		nexus.Error(w, http.StatusBadRequest, "national_code талбарыг заана уу")
		return
	}

	var code *string
	// The table name comes from the switch above, never from the request.
	err = m.db.QueryRow(r.Context(),
		`UPDATE `+table+` SET national_code = NULLIF($2, ''), updated_at = NOW()
		  WHERE id = $1::uuid RETURNING national_code`, id, *patch.NationalCode).
		Scan(&code)
	if errors.Is(err, pgx.ErrNoRows) {
		nexus.Error(w, http.StatusNotFound, "ийм объект олдсонгүй")
		return
	}
	if isUniqueViolation(err) {
		nexus.Error(w, http.StatusConflict, "энэ улсын дугаар өөр объектод бүртгэгдсэн байна")
		return
	}
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not record the code")
		return
	}

	nexus.Audit(r.Context(), tenantID, claims.UserID, "petro.registry.coded", id,
		map[string]any{"kind": kind, "national_code": derefOrEmpty(code)})

	nexus.JSON(w, http.StatusOK, map[string]any{"id": id, "kind": kind, "national_code": code})
}

// CensusPatch is what an operator answers about one forecourt.
//
// IntegrationClass is a single letter and the whole point of the exercise:
//
//	A  its own POS, integrated over an API
//	B  a controller a driver can talk to
//	C  pulse counters only
//	D  mechanical totalisers — a person reads them
//
// The February driver order falls out of counting these.
// Pointers throughout, so a patch that mentions one field leaves the rest
// alone. They were plain strings, and a follow-up {"has_internet": true} blanked
// the class, the brand and the protocol while stamping census_at — the index
// said "surveyed" and the summary counted it as "not surveyed" (audit §21).
// Every other patch handler in this module already works this way.
type CensusPatch struct {
	IntegrationClass *string `json:"integration_class"`
	PumpBrand        *string `json:"pump_brand"`
	PumpProtocol     *string `json:"pump_protocol"`
	HasATG           *bool   `json:"has_atg"`
	HasInternet      *bool   `json:"has_internet"`
}

var integrationClasses = map[string]bool{"A": true, "B": true, "C": true, "D": true}

// handleCensus records the technical survey of one station.
func (m *Module) handleCensus(w http.ResponseWriter, r *http.Request) {
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

	var patch CensusPatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&patch); err != nil {
		nexus.Error(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if patch.IntegrationClass != nil && *patch.IntegrationClass != "" &&
		!integrationClasses[*patch.IntegrationClass] {
		nexus.Error(w, http.StatusBadRequest, "ангилал нь A, B, C, D-ийн нэг байна")
		return
	}

	var name, class string
	err = m.db.QueryRow(r.Context(), `
		UPDATE petro_stations
		   SET integration_class = COALESCE(NULLIF($2, '')::char(1), integration_class),
		       pump_brand    = COALESCE($3, pump_brand),
		       pump_protocol = COALESCE($4, pump_protocol),
		       has_atg       = COALESCE($5, has_atg),
		       has_internet  = COALESCE($6, has_internet),
		       census_at = NOW(), updated_at = NOW()
		 WHERE id = $1::uuid
		RETURNING name, COALESCE(integration_class, '')`,
		id, derefOrEmpty(patch.IntegrationClass), patch.PumpBrand, patch.PumpProtocol,
		patch.HasATG, patch.HasInternet).Scan(&name, &class)
	if errors.Is(err, pgx.ErrNoRows) {
		nexus.Error(w, http.StatusNotFound, "ийм ШТС олдсонгүй")
		return
	}
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not record the census")
		return
	}

	nexus.Audit(r.Context(), tenantID, claims.UserID, "petro.census.recorded", id,
		map[string]any{"name": name, "class": class,
			"protocol": derefOrEmpty(patch.PumpProtocol)})

	nexus.JSON(w, http.StatusOK, map[string]any{
		"id": id, "name": name, "integration_class": class,
	})
}

// CensusSummary is what the census is for: how many of each class, and how many
// forecourts nobody has surveyed yet.
type CensusSummary struct {
	Class       string `json:"class"`
	Label       string `json:"label"`
	Stations    int    `json:"stations"`
	WithATG     int    `json:"with_atg"`
	WithNetwork int    `json:"with_internet"`
}

var classLabels = map[string]string{
	"A": "Өөрийн POS — API-аар",
	"B": "Controller-той",
	"C": "Импульс тоолуур",
	"D": "Механик — гар оруулалт",
	"":  "Тооллого хийгээгүй",
}

// handleCensusSummary answers the survey so far.
//
// Visible to a supervisory body across the country and to an operator for
// their own network; the row-level policy decides which, and the query is the
// same either way.
func (m *Module) handleCensusSummary(w http.ResponseWriter, r *http.Request) {
	if _, ok := nexus.RequireWorkspace(w, r); !ok {
		return
	}

	rows, err := m.db.Query(r.Context(), `
		SELECT COALESCE(integration_class, ''), COUNT(*)::int,
		       COUNT(*) FILTER (WHERE has_atg)::int,
		       COUNT(*) FILTER (WHERE has_internet)::int
		  FROM petro_stations
		 WHERE registry_status <> 'closed'
		 GROUP BY 1
		 ORDER BY 1`)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the census")
		return
	}
	defer rows.Close()

	out := []CensusSummary{}
	surveyed, total := 0, 0
	for rows.Next() {
		var c CensusSummary
		if err := rows.Scan(&c.Class, &c.Stations, &c.WithATG, &c.WithNetwork); err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not read the census")
			return
		}
		c.Label = classLabels[c.Class]
		total += c.Stations
		if c.Class != "" {
			surveyed += c.Stations
		}
		out = append(out, c)
	}

	nexus.JSON(w, http.StatusOK, map[string]any{
		"classes": out, "surveyed": surveyed, "stations_total": total,
	})
}

// ------------------------------------------------------- oversight bodies

// OversightBody is one organisation that supervises the rest.
type OversightBody struct {
	TenantID string `json:"tenant_id"`
	Name     string `json:"name"`
	Scope    string `json:"scope"`
	Aimag    string `json:"aimag"`
	AddedAt  string `json:"added_at"`
}

var oversightScopes = map[string]bool{
	"national": true, "tax": true, "customs": true, "aimag": true, "audit": true,
}

// handleOversightBodies lists and appoints supervisory organisations.
//
// Console-only, through the same session check the operator overview makes:
// appointing a regulator is a deployment act, not a tenant's own setting, and
// an endpoint inside the tenant gate would let an organisation appoint itself.
//
// Reading the list takes any console session; appointing takes a role that may
// change the deployment. The status check alone let an auditor or a support
// operator hand a company read access over all of its competitors.
func (m *Module) handleOversightBodies(w http.ResponseWriter, r *http.Request) {
	status, role := operatorSession(r)
	if status != http.StatusOK {
		nexus.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	if r.Method == http.MethodPost {
		if role != "superadmin" && role != "operator" {
			nexus.Error(w, http.StatusForbidden, "хяналтын байгууллага томилох эрхгүй")
			return
		}
		var body OversightBody
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&body); err != nil {
			nexus.Error(w, http.StatusBadRequest, "invalid payload")
			return
		}
		if body.Scope == "" {
			body.Scope = "national"
		}
		if !oversightScopes[body.Scope] {
			nexus.Error(w, http.StatusBadRequest,
				"хамрах хүрээ нь national, tax, customs, aimag, audit-ийн нэг байна")
			return
		}
		// A province body with no province sees nothing (migration 00018) —
		// refused here rather than appointed into an empty screen.
		aimag, known := normalizeAimag(body.Aimag)
		if !known {
			nexus.Error(w, http.StatusBadRequest, aimagMessage)
			return
		}
		body.Aimag = aimag
		if body.Scope == "aimag" && body.Aimag == "" {
			nexus.Error(w, http.StatusBadRequest, "аймгийн хяналтын байгууллагад аймгийг заана уу")
			return
		}
		if !isUUID(body.TenantID) {
			nexus.Error(w, http.StatusBadRequest, "байгууллагын id буруу")
			return
		}
		tag, err := m.db.Exec(r.Context(), `
			INSERT INTO petro_oversight_bodies (tenant_id, name, scope, aimag)
			SELECT t.id, $2, $3, $4 FROM registry.tenants t WHERE t.id = $1::uuid
			ON CONFLICT (tenant_id) DO UPDATE
			   SET name = EXCLUDED.name, scope = EXCLUDED.scope, aimag = EXCLUDED.aimag`,
			body.TenantID, body.Name, body.Scope, body.Aimag)
		if err != nil {
			slog.Error("petro: appoint oversight body", "tenant_id", body.TenantID, "error", err)
			nexus.Error(w, http.StatusInternalServerError, "could not appoint the body")
			return
		}
		if tag.RowsAffected() == 0 {
			nexus.Error(w, http.StatusNotFound, "ийм байгууллага олдсонгүй")
			return
		}
		// Who can see across every company is the most consequential setting
		// this module has; it is written down even though the console session
		// carries no platform user to attribute it to.
		slog.Warn("petro: oversight body appointed", "tenant_id", body.TenantID,
			"scope", body.Scope, "aimag", body.Aimag, "operator_role", role)
	}

	rows, err := m.db.Query(r.Context(), `
		SELECT o.tenant_id::text, COALESCE(NULLIF(o.name, ''), t.name), o.scope,
		       o.aimag, o.added_at::text
		  FROM petro_oversight_bodies o
		  JOIN registry.tenants t ON t.id = o.tenant_id
		 ORDER BY o.added_at`)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the bodies")
		return
	}
	defer rows.Close()

	out := []OversightBody{}
	for rows.Next() {
		var b OversightBody
		if err := rows.Scan(&b.TenantID, &b.Name, &b.Scope, &b.Aimag, &b.AddedAt); err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not read the bodies")
			return
		}
		out = append(out, b)
	}
	nexus.JSON(w, http.StatusOK, map[string]any{"bodies": out})
}

func derefOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
