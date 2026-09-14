/*
 * Gerege Nexus
 * Copyright (c) 2026 Gerege Systems Development Team, Gerege Nomadica Foundation
 * Distributed under the Apache 2.0 License.
 *
 * The national picture, and the three questions asked of it.
 *
 *	how much is there          — stock, by grade, by province, in days
 *	who did not say            — coverage, and the list of names behind it
 *	where does it not add up   — the balances, and the lines that broke them
 *
 * # Coverage comes first, and that is a finding rather than a layout choice
 *
 * Ghana ran the most complete downstream monitoring stack on the continent and
 * its 2025 audit found the software working and the coverage fallen. A stock
 * figure computed from sixty per cent of forecourts is not a stock figure with
 * some uncertainty — it is a different number, quietly. So every answer here
 * carries sites_total beside sites_reported, and the screens put it in the
 * first row rather than in a footnote.
 *
 * # Why the day table exists
 *
 * The dashboard has three seconds (§4.5). Recomputing the country from the
 * line table on every open would spend them in one query, and would spend them
 * again for every official who opens it at nine in the morning. So a day is
 * computed once, into petro_daily_national, and every read is a lookup. The
 * refresh is idempotent per day: a report that arrives late rewrites its own
 * day and nothing else.
 *
 * # These queries run without a tenant
 *
 * The refresh and the public endpoint run outside the workspace middleware, on
 * the login role, where the row-level policies do not apply — which is the only
 * way to sum a country. Everything they emit is an aggregate. Nothing on this
 * path may ever return a company's own row: the same rule metrics.go states,
 * for the same reason.
 */

package petro

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gerege-systems/open-gerege-nexus/backend/pkg/nexus"
	"github.com/jackc/pgx/v5"
)

// RefreshDaily recomputes one day of the national aggregate.
//
// Exported so a scheduled job, a backfill command and the manual button on the
// oversight screen all take the same path — three callers, one definition of
// what the country's numbers are.
func (m *Module) RefreshDaily(ctx context.Context, day time.Time) error {
	// Through petro_refresh_daily rather than a statement here, and the reason
	// is that the button never worked: the handler runs inside the workspace
	// gate, so the connection is bound to the tenant role, and migration 00010
	// granted that role SELECT on petro_daily_national and nothing else. Every
	// press answered 42501. The function is SECURITY DEFINER, checks the
	// caller itself, and deletes the day before rewriting it — which also
	// retires the ghost rows a pure upsert left behind (audit §5, §23).
	_, err := m.db.Exec(ctx, `SELECT petro_refresh_daily($1::date)`, day.Format("2006-01-02"))
	return err
}

// NationalRow is one grade in one province on one day.
type NationalRow struct {
	Day            string   `json:"day"`
	ProductCode    string   `json:"product_code"`
	ProductLabel   string   `json:"product_label"`
	Aimag          string   `json:"aimag"`
	StockLiters    float64  `json:"stock_liters"`
	CapacityLiters float64  `json:"capacity_liters"`
	ReceiptsLiters float64  `json:"receipts_liters"`
	SalesLiters    float64  `json:"sales_liters"`
	SitesTotal     int      `json:"sites_total"`
	SitesReported  int      `json:"sites_reported"`
	DaysOfSupply   *float64 `json:"days_of_supply"`

	// Carried while the product rows are being summed; not serialised.
	daysOfSupplySum    float64 `json:"-"`
	daysOfSupplyWeight float64 `json:"-"`
}

// handleNationalDashboard answers the state's picture of the country.
func (m *Module) handleNationalDashboard(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := m.requireOversight(w, r); !ok {
		return
	}

	day, ok := dayParam(w, r)
	if !ok {
		return
	}
	if day == "" {
		if err := m.db.QueryRow(r.Context(),
			`SELECT COALESCE(MAX(day)::text, CURRENT_DATE::text) FROM petro_daily_national`).
			Scan(&day); err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not find the latest day")
			return
		}
	}

	rows, err := m.db.Query(r.Context(), `
		SELECT n.day::text, n.product_code, pr.label_mn, n.aimag,
		       n.stock_liters::float8, n.capacity_liters::float8,
		       n.receipts_liters::float8, n.sales_liters::float8,
		       n.sites_total, n.sites_reported, n.days_of_supply::float8
		  FROM petro_daily_national n
		  JOIN petro_products pr ON pr.code = n.product_code
		 WHERE n.day = $1::date
		 ORDER BY pr.sort_order, n.aimag`, day)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the national table")
		return
	}
	defer rows.Close()

	detail := []NationalRow{}
	byProduct := map[string]*NationalRow{}
	productOrder := []string{}
	var sitesTotal, sitesReported int

	for rows.Next() {
		var n NationalRow
		if err := rows.Scan(&n.Day, &n.ProductCode, &n.ProductLabel, &n.Aimag,
			&n.StockLiters, &n.CapacityLiters, &n.ReceiptsLiters, &n.SalesLiters,
			&n.SitesTotal, &n.SitesReported, &n.DaysOfSupply); err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not read the national table")
			return
		}
		detail = append(detail, n)

		agg, seen := byProduct[n.ProductCode]
		if !seen {
			row := n
			row.Aimag = ""
			byProduct[n.ProductCode] = &row
			productOrder = append(productOrder, n.ProductCode)
		} else {
			agg.StockLiters += n.StockLiters
			agg.CapacityLiters += n.CapacityLiters
			agg.ReceiptsLiters += n.ReceiptsLiters
			agg.SalesLiters += n.SalesLiters
			agg.SitesTotal += n.SitesTotal
			agg.SitesReported += n.SitesReported
		}
		// Weighted by the stock it describes: a province holding a million
		// litres and one holding a thousand must not count equally toward the
		// national figure.
		if n.DaysOfSupply != nil && n.StockLiters > 0 {
			byProduct[n.ProductCode].daysOfSupplySum += *n.DaysOfSupply * n.StockLiters
			byProduct[n.ProductCode].daysOfSupplyWeight += n.StockLiters
		}
	}

	// Sites are counted once, from the register, rather than by summing the
	// per-grade rows.
	if err := m.db.QueryRow(r.Context(), `
		WITH sites AS (
			SELECT id FROM petro_stations WHERE registry_status <> 'closed'
			UNION ALL
			SELECT id FROM petro_depots WHERE registry_status <> 'closed')
		SELECT count(*)::int FROM sites`).Scan(&sitesTotal); err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not count the register")
		return
	}
	if err := m.db.QueryRow(r.Context(), `
		SELECT count(DISTINCT (l.site_kind, l.site_id))::int
		  FROM petro_report_lines l
		  JOIN petro_report_submissions s ON s.id = l.submission_id
		  JOIN petro_report_periods p ON p.id = s.period_id
		 WHERE p.period_start = $1::date AND s.status IN ('submitted', 'approved')`,
		day).Scan(&sitesReported); err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not count the reporters")
		return
	}

	// Days of supply comes from the stored column, summed, and is not
	// recomputed here.
	//
	// It used to be `stock / sales_today`, while the column it was overwriting
	// held `stock / seven-day average`. The two disagreed inside one response:
	// a Sunday, when a grade sells a third of its weekly average, showed twelve
	// days in the province rows and thirty-six in the product rows — and the
	// larger one was the headline, and the one published as open data (audit
	// §24). One definition, in the table, where the refresh put it.
	products := make([]NationalRow, 0, len(productOrder))
	for _, code := range productOrder {
		p := byProduct[code]
		if p.daysOfSupplyWeight > 0 {
			dos := p.daysOfSupplySum / p.daysOfSupplyWeight
			p.DaysOfSupply = &dos
		} else {
			p.DaysOfSupply = nil
		}
		products = append(products, *p)
	}

	// Coverage counts forecourts, not forecourt×grade pairs.
	//
	// The table is keyed by (day, product, province), so summing sites_total
	// across every grade counted a station that sells three grades three times:
	// a country of five hundred stations reported "412 of 1,500" — the number
	// the file's own header calls the first row on the screen, three times
	// larger than the register (audit §25).
	coverage := 0.0
	if sitesTotal > 0 {
		coverage = float64(sitesReported) / float64(sitesTotal) * 100
	}

	nexus.JSON(w, http.StatusOK, map[string]any{
		"day": day,
		"coverage": map[string]any{
			"sites_total": sitesTotal, "sites_reported": sitesReported, "percent": coverage,
		},
		"products": products,
		"detail":   detail,
	})
}

// GapRow is one company that has not answered.
type GapRow struct {
	TenantID     string  `json:"tenant_id"`
	TenantName   string  `json:"tenant_name"`
	SitesTotal   int     `json:"sites_total"`
	LastReported *string `json:"last_reported_at"`
	Overdue      bool    `json:"overdue"`
}

// handleGaps answers who did not report for the newest period that is past
// due, which is the single cheapest thing this system does.
//
// A company with no sites at all is excluded: it has nothing to report, and a
// list padded with names that were never expected is a list nobody reads.
func (m *Module) handleGaps(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := m.requireOversight(w, r); !ok {
		return
	}

	var periodID, periodStart string
	err := m.db.QueryRow(r.Context(), `
		SELECT id::text, period_start::text
		  FROM petro_report_periods
		 WHERE due_at < NOW()
		 ORDER BY period_start DESC
		 LIMIT 1`).Scan(&periodID, &periodStart)
	if errors.Is(err, pgx.ErrNoRows) {
		nexus.JSON(w, http.StatusOK, map[string]any{"period": nil, "missing": []GapRow{}})
		return
	}
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not find the period")
		return
	}

	rows, err := m.db.Query(r.Context(), `
		WITH holders AS (
			SELECT tenant_id, COUNT(*)::int AS sites FROM (
				SELECT tenant_id FROM petro_stations WHERE registry_status <> 'closed'
				UNION ALL
				SELECT tenant_id FROM petro_depots WHERE registry_status <> 'closed') s
			 GROUP BY tenant_id
		)
		SELECT h.tenant_id::text, t.name, h.sites,
		       (SELECT MAX(s2.submitted_at)::text FROM petro_report_submissions s2
		         WHERE s2.tenant_id = h.tenant_id)
		  FROM holders h
		  JOIN registry.tenants t ON t.id = h.tenant_id
		 WHERE NOT EXISTS (
		       SELECT 1 FROM petro_report_submissions s
		        WHERE s.tenant_id = h.tenant_id AND s.period_id = $1::uuid
		          AND s.status IN ('submitted', 'approved'))
		 ORDER BY h.sites DESC, t.name`, periodID)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the gaps")
		return
	}
	defer rows.Close()

	missing := []GapRow{}
	for rows.Next() {
		var g GapRow
		if err := rows.Scan(&g.TenantID, &g.TenantName, &g.SitesTotal, &g.LastReported); err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not read the gaps")
			return
		}
		g.Overdue = true
		missing = append(missing, g)
	}
	if err := rows.Err(); err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the rows")
		return
	}

	nexus.JSON(w, http.StatusOK, map[string]any{
		"period":  map[string]string{"id": periodID, "period_start": periodStart},
		"missing": missing,
	})
}

// BalanceRow is one of the reconciliation ladder's rungs.
type BalanceRow struct {
	Code         string   `json:"code"`
	Name         string   `json:"name"`
	TolerancePct float64  `json:"tolerance_pct"`
	LeftLiters   float64  `json:"left_liters"`
	RightLiters  float64  `json:"right_liters"`
	DeltaLiters  float64  `json:"delta_liters"`
	DeltaPct     *float64 `json:"delta_pct"`
	Breaches     int      `json:"breaches"`
	Available    bool     `json:"available"`
	Waiting      string   `json:"waiting,omitempty"`
}

// handleReconciliation answers the five balances for a window.
//
// Two of them are computable from typed-in reports and three are not; the
// three still appear, marked, with what they are waiting for. A ladder that
// showed only the rungs that are built would let everyone forget the rest
// exists.
func (m *Module) handleReconciliation(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := m.requireOversight(w, r); !ok {
		return
	}
	pol := m.LoadPolicy(r.Context())

	days := 7
	// nexus.Now(), not time.Now(): the window is a run of calendar days in
	// Ulaanbaatar, and the container's clock is UTC. Between midnight and eight
	// in the morning the two disagree by a day, which is exactly when a night
	// shift's figures are being looked at.
	from := nexus.Now().AddDate(0, 0, -days).Format("2006-01-02")

	// ΔB — what left a depot against what a forecourt signed for, from the
	// movements closed in the window.
	var declared, received float64
	var breachesB int
	if err := m.db.QueryRow(r.Context(), `
		SELECT COALESCE(SUM(declared_liters), 0)::float8,
		       COALESCE(SUM(received_liters), 0)::float8,
		       COUNT(*) FILTER (WHERE ABS(COALESCE(variance_pct, 0)) > $2)::int
		  FROM petro_movements
		 WHERE status IN ('closed', 'disputed')
		   AND closed_at >= $1::date`, from, pol.TransportTolerancePct).
		Scan(&declared, &received, &breachesB); err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not compute the transport balance")
		return
	}

	// ΔC — the station balance, summed from the residual every line already
	// carries. Summed rather than recomputed: the dashboard and the finding
	// must not be able to disagree.
	var throughput, residual float64
	var breachesC int
	if err := m.db.QueryRow(r.Context(), `
		SELECT COALESCE(SUM(l.opening_liters + l.receipts_liters), 0)::float8,
		       COALESCE(SUM(l.variance_liters), 0)::float8,
		       COUNT(*) FILTER (WHERE ABS(COALESCE(l.variance_pct, 0)) > $2)::int
		  FROM petro_report_lines l
		  JOIN petro_report_submissions s ON s.id = l.submission_id
		  JOIN petro_report_periods p ON p.id = s.period_id
		 WHERE p.period_start >= $1::date
		   AND s.status IN ('submitted', 'approved')`, from, pol.StationTolerancePct).
		Scan(&throughput, &residual, &breachesC); err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not compute the station balance")
		return
	}

	pct := func(delta, base float64) *float64 {
		if base == 0 {
			return nil
		}
		v := delta / base * 100
		return &v
	}

	balances := []BalanceRow{
		{Code: "ΔA", Name: "Гаальд мэдүүлсэн − Терминалд хүлээн авсан",
			TolerancePct: pol.BorderTolerancePct, Available: false,
			Waiting: "гаалийн интеграц — 2-р сар"},
		{Code: "ΔB", Name: "Терминалаас ачсан − ШТС-д хүлээн авсан",
			TolerancePct: pol.TransportTolerancePct, Available: true,
			LeftLiters: declared, RightLiters: received,
			DeltaLiters: declared - received, DeltaPct: pct(declared-received, declared),
			Breaches: breachesB},
		{Code: "ΔC", Name: "Хүлээн авсан − Түгээсэн − Үлдэгдлийн өөрчлөлт",
			TolerancePct: pol.StationTolerancePct, Available: true,
			LeftLiters: throughput, RightLiters: throughput - residual,
			DeltaLiters: residual, DeltaPct: pct(residual, throughput),
			Breaches: breachesC},
		{Code: "ΔD", Name: "Түгээсэн − e-Barimt-аар бүртгэгдсэн",
			TolerancePct: 0, Available: false, Waiting: "POS ба e-Barimt — 3-р сар"},
		{Code: "ΔE", Name: "Ваучераар олгосон − Түгээсэн",
			TolerancePct: 0.2, Available: false, Waiting: "ваучерын горим — 3-р сар"},
	}

	nexus.JSON(w, http.StatusOK, map[string]any{
		"from": from, "days": days, "balances": balances,
	})
}

// handleRefreshDaily recomputes a day on demand.
func (m *Module) handleRefreshDaily(w http.ResponseWriter, r *http.Request) {
	tenantID, claims, ok := m.requireOversight(w, r)
	if !ok {
		return
	}

	// The same calendar the scheduled refresh uses (jobs.go); on UTC, time.Now()
	// would recompute yesterday for anybody pressing the button before 08:00.
	day := nexus.Now()
	if v := r.URL.Query().Get("day"); v != "" {
		parsed, err := time.Parse("2006-01-02", v)
		if err != nil {
			nexus.Error(w, http.StatusBadRequest, "огноо YYYY-MM-DD хэлбэртэй байна")
			return
		}
		day = parsed
	}

	if err := m.RefreshDaily(r.Context(), day); err != nil {
		if isInsufficientPrivilege(err) {
			nexus.Error(w, http.StatusForbidden, oversightReadOnlyMessage)
			return
		}
		slog.Error("petro: daily refresh failed", "error", err)
		nexus.Error(w, http.StatusInternalServerError, "could not refresh the national table")
		return
	}

	nexus.Audit(r.Context(), tenantID, claims.UserID, "petro.national.refreshed",
		day.Format("2006-01-02"), nil)

	nexus.JSON(w, http.StatusOK, map[string]any{"day": day.Format("2006-01-02"), "refreshed": true})
}

// handlePublicDaily is the open-data endpoint.
//
// National totals by grade, and the coverage they were computed from. No
// company, no province-level breakdown of a single operator's stock, nothing a
// competitor could act on — the same line metrics.go draws.
//
// Coverage is published beside the total on purpose. A number without the
// share of the country it came from invites everyone to treat it as complete,
// and it is the one thing a reader outside the system cannot check.
func (m *Module) handlePublicDaily(w http.ResponseWriter, r *http.Request) {
	day, ok := dayParam(w, r)
	if !ok {
		return
	}
	if day == "" {
		if err := m.db.QueryRow(r.Context(),
			`SELECT MAX(day)::text FROM petro_daily_national`).Scan(&day); err != nil || day == "" {
			nexus.JSON(w, http.StatusOK, map[string]any{"day": nil, "products": []any{}})
			return
		}
	}

	// The same two corrections the oversight dashboard needed: days of supply
	// is the stored column, weighted by the stock it describes, and the site
	// counts come from the rows rather than from summing them across grades.
	rows, err := m.db.Query(r.Context(), `
		SELECT n.product_code, pr.label_mn,
		       SUM(n.stock_liters)::float8, SUM(n.capacity_liters)::float8,
		       SUM(n.sales_liters)::float8,
		       (SELECT count(*)::int FROM (
		           SELECT id FROM petro_stations WHERE registry_status <> 'closed'
		           UNION ALL
		           SELECT id FROM petro_depots WHERE registry_status <> 'closed') s),
		       (SELECT count(DISTINCT (l.site_kind, l.site_id))::int
		          FROM petro_report_lines l
		          JOIN petro_report_submissions sub ON sub.id = l.submission_id
		          JOIN petro_report_periods p ON p.id = sub.period_id
		         WHERE p.period_start = $1::date
		           AND sub.status IN ('submitted', 'approved')
		           AND l.product_code = n.product_code),
		       CASE WHEN SUM(n.stock_liters) FILTER (WHERE n.days_of_supply IS NOT NULL) > 0
		            THEN SUM(n.days_of_supply * n.stock_liters)
		                 / SUM(n.stock_liters) FILTER (WHERE n.days_of_supply IS NOT NULL)
		       END::float8
		  FROM petro_daily_national n
		  JOIN petro_products pr ON pr.code = n.product_code
		 WHERE n.day = $1::date
		 GROUP BY n.product_code, pr.label_mn, pr.sort_order
		 ORDER BY pr.sort_order`, day)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the daily figures")
		return
	}
	defer rows.Close()

	type publicRow struct {
		ProductCode   string   `json:"product_code"`
		ProductLabel  string   `json:"product_label"`
		StockLiters   float64  `json:"stock_liters"`
		CapacityL     float64  `json:"capacity_liters"`
		SalesLiters   float64  `json:"sales_liters"`
		SitesTotal    int      `json:"sites_total"`
		SitesReported int      `json:"sites_reported"`
		DaysOfSupply  *float64 `json:"days_of_supply"`
	}

	out := []publicRow{}
	for rows.Next() {
		var p publicRow
		if err := rows.Scan(&p.ProductCode, &p.ProductLabel, &p.StockLiters, &p.CapacityL,
			&p.SalesLiters, &p.SitesTotal, &p.SitesReported, &p.DaysOfSupply); err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not read the daily figures")
			return
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read the daily figures")
		return
	}

	nexus.JSON(w, http.StatusOK, map[string]any{"day": day, "products": out})
}

// dayParam reads the optional ?day=YYYY-MM-DD.
//
// Checked here rather than left to `$1::date`: a malformed value used to reach
// Postgres, fail with 22007 and come back as a 500 — on the public endpoint too,
// where anybody could fill the error log with it.
func dayParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	day := r.URL.Query().Get("day")
	if day == "" {
		return "", true
	}
	if _, err := time.Parse("2006-01-02", day); err != nil {
		nexus.Error(w, http.StatusBadRequest, "огноо YYYY-MM-DD хэлбэртэй байна")
		return "", false
	}
	return day, true
}
