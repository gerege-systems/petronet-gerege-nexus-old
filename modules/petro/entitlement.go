/*
 * Gerege Nexus
 * Copyright (c) 2026 Gerege Systems Development Team, Gerege Nomadica Foundation
 * Distributed under the Apache 2.0 License.
 *
 * A citizen's daily fuel entitlement, and the vouchers drawn against it.
 *
 * # The rule
 *
 * Fifty thousand tugrik a day, per person, spendable at any pump. Three things
 * follow from that sentence and each one is a departure from the system this
 * replaces:
 *
 *	in tugrik      a litre is worth different amounts of ration depending on
 *	               which fuel it is, and prices move; money does not have that
 *	               problem
 *	per person     the old rule was per vehicle plate, so somebody with three
 *	               cars had three rations and somebody borrowing a car had none
 *	any pump       so a voucher reserves nothing at any station — reserving
 *	               stock somewhere a person may never go makes the national
 *	               stock figure wrong, which is the figure the whole thing is
 *	               supposed to report
 *
 * # Who a citizen is
 *
 * The platform user signed in on this request. Never a value from the body:
 * the system this replaces took the registry number out of the request and
 * invented one when it was missing, labelling the result "eID verified", which
 * meant anybody with curl could mint rations for any plate. The identity here
 * is the session's and the session's only.
 *
 * Citizens reach the platform through eID just-in-time provisioning
 * (EID_JIT_TENANT_SLUG), so they are all members of one organisation. That is
 * what makes them ordinary users with ordinary sessions — and it is also why
 * the row-level policy on these tables cannot be what separates one citizen
 * from another. See the migration.
 */

package petro

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gerege-systems/open-gerege-nexus/backend/pkg/nexus"
	"github.com/jackc/pgx/v5"
)

// The ration, in tugrik per citizen per day.
//
// Configurable rather than constant, and read per request rather than at boot.
// A ration is a policy decision — it moves when supply does — and a number that
// needs a rebuild to change is a number somebody will work around instead.
//
// FUEL_DAILY_GRANT_MNT overrides it. Development sets it high so a tester is
// not blocked by the rule they are trying to exercise; production leaves it
// alone. The mechanism is identical either way, which is the point: a limit
// that is switched off for testing is a limit nobody has tested.
const defaultDailyGrantMNT = 50_000

func dailyGrantMNT() float64 {
	if raw := os.Getenv("FUEL_DAILY_GRANT_MNT"); raw != "" {
		if value, err := strconv.ParseFloat(raw, 64); err == nil && value > 0 {
			return value
		}
		slog.Warn("FUEL_DAILY_GRANT_MNT is not a positive number; using the default",
			"value", raw, "default", defaultDailyGrantMNT)
	}
	return defaultDailyGrantMNT
}

// What each fuel is called.
//
// Here rather than looked up from an operator's inventory, and the reason is
// the row-level policy: a citizen belongs to the citizens organisation, station
// inventory belongs to the companies, and a SELECT run in the citizen's context
// sees none of it. The first version joined that table and every voucher came
// back labelled "ai92" — correct, and not what anybody calls it.
//
// A fuel grade is national anyway. АИ-92 is АИ-92 whoever is selling it, so it
// was never an operator's to name.
var fuelLabels = map[string]string{
	"ai80":         "АИ-80",
	"ai92":         "АИ-92",
	"ai95":         "АИ-95",
	"ai98":         "АИ-98",
	"diesel":       "Дизель (ДТ)",
	"euro5_diesel": "Euro-5 ДТ",
	"euro92":       "Euro-92",
	"lpg":          "Газ (LPG)",
}

// fuelLabel is the grade's name, or the code when this deployment does not sell
// it — a code on screen is ugly and an empty label is a blank nobody can read.
func fuelLabel(code string) string {
	if label, ok := fuelLabels[code]; ok {
		return label
	}
	return code
}

// VoucherLifetime is how long a voucher stays good for.
//
// Long enough to drive across the city in traffic and queue at the far end,
// short enough that an unused one returns to the day's balance while the day
// still has hours in it.
const VoucherLifetime = 3 * time.Hour

// Entitlement is what a citizen has left today.
type Entitlement struct {
	Date        string  `json:"date"`
	GrantedMNT  float64 `json:"granted_mnt"`
	UsedMNT     float64 `json:"used_mnt"`
	RemainigMNT float64 `json:"remaining_mnt"`
}

// Voucher is a claim against the day's entitlement.
type Voucher struct {
	ID          string     `json:"id"`
	AmountMNT   float64    `json:"amount_mnt"`
	FuelType    string     `json:"fuel_type"`
	FuelLabel   string     `json:"fuel_label"`
	QRToken     string     `json:"qr_token"`
	Status      string     `json:"status"`
	ExpiresAt   time.Time  `json:"expires_at"`
	CreatedAt   time.Time  `json:"created_at"`
	StationName string     `json:"intended_station,omitempty"`
	RedeemedAt  *time.Time `json:"redeemed_at,omitempty"`
}

// citizen is the person this request is for.
//
// The session, and nothing else. A handler that needs to know who somebody is
// asks here rather than reading a field, so there is exactly one answer and one
// place to change if the answer ever comes from somewhere new.
func citizen(r *http.Request) (id string, tenantID string, err error) {
	claims, err := nexus.UserFromContext(r.Context())
	if err != nil {
		return "", "", err
	}
	tenant, err := nexus.WorkspaceID(r.Context())
	if err != nil {
		return "", "", err
	}
	return claims.UserID, tenant, nil
}

// handleMyEntitlement answers what today's ration has left in it.
//
// Reading does not create the row. A day nobody has drawn against has no row,
// and reporting the full grant for it is the same answer the row would give —
// writing one on a GET would mean a page load counted as taking part.
func (m *Module) handleMyEntitlement(w http.ResponseWriter, r *http.Request) {
	citizenID, _, err := citizen(r)
	if err != nil {
		nexus.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	grant := dailyGrantMNT()
	entitlement := Entitlement{
		Date:        nexus.Today().Format("2006-01-02"),
		GrantedMNT:  grant,
		RemainigMNT: grant,
	}

	// Expired vouchers are settled on the way in.
	//
	// The sweep ran only when a voucher was issued, so a citizen who took
	// thirty thousand and never spent it saw "20,000 ₮ remaining" three hours
	// later while actually holding fifty — and the screen filters its amount
	// buttons by that figure, so they were shown less than they had and,
	// below the smallest button, nothing at all (audit §37). Reading the
	// entitlement is the moment the answer has to be true.
	if err := m.settleExpiredVouchers(r.Context(), citizenID); err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not settle expired vouchers")
		return
	}

	err = m.db.QueryRow(r.Context(), `
		SELECT granted_mnt::float8, used_mnt::float8
		  FROM petro_entitlements
		 WHERE citizen_id = $1 AND for_date = CURRENT_DATE`,
		citizenID).Scan(&entitlement.GrantedMNT, &entitlement.UsedMNT)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		nexus.Error(w, http.StatusInternalServerError, "could not read today's entitlement")
		return
	}
	entitlement.RemainigMNT = entitlement.GrantedMNT - entitlement.UsedMNT

	nexus.JSON(w, http.StatusOK, entitlement)
}

// IssueVoucherRequest is what a citizen asks for.
//
// No identity fields. There is nowhere to put one, which is the point.
type IssueVoucherRequest struct {
	AmountMNT         float64 `json:"amount_mnt"`
	FuelType          string  `json:"fuel_type"`
	IntendedStationID string  `json:"intended_station_id"`
}

// handleIssueVoucher draws an amount out of today's entitlement.
//
// # The whole thing is one transaction, and the limit is one statement
//
// The check and the deduction are the same UPDATE. Written as a read followed
// by a write they are two, and two concurrent requests both read a balance that
// covers them and both write — which is exactly the bug the system this
// replaces had in its anti-hoarding check, where an unlocked COUNT let two
// simultaneous requests each see zero active vouchers.
//
// The database refuses an overdraw even if this statement is ever wrong: the
// table carries a CHECK that used_mnt cannot exceed granted_mnt.
func (m *Module) handleIssueVoucher(w http.ResponseWriter, r *http.Request) {
	citizenID, tenantID, err := citizen(r)
	if err != nil {
		nexus.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var request IssueVoucherRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
		nexus.Error(w, http.StatusBadRequest, "invalid payload")
		return
	}
	grant := dailyGrantMNT()
	if request.AmountMNT <= 0 || request.AmountMNT > grant {
		nexus.Error(w, http.StatusBadRequest, "дүн 0-ээс их, өдрийн эрхээс бага байх ёстой")
		return
	}
	if _, known := fuelLabels[request.FuelType]; !known {
		nexus.Error(w, http.StatusBadRequest, "энэ түлшний төрлийг мэдэхгүй байна")
		return
	}

	tx, err := m.db.Begin(r.Context())
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not start")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()

	// Expire anything of this citizen's that has run out, before the balance is
	// read. An unused voucher holds ration that should come back to them, and
	// the moment it comes back is the moment they next ask for one.
	if _, err := tx.Exec(r.Context(), settleExpiredSQL, citizenID); err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not settle expired vouchers")
		return
	}

	// Take the money. One statement: the WHERE is the limit.
	var granted, used float64
	err = tx.QueryRow(r.Context(), `
		INSERT INTO petro_entitlements (citizen_id, tenant_id, for_date, granted_mnt, used_mnt)
		VALUES ($1, $2, CURRENT_DATE, $3, $4)
		ON CONFLICT (citizen_id, for_date) DO UPDATE
		   SET used_mnt = petro_entitlements.used_mnt + $4,
		       -- The day's grant follows the deployment's current figure, but
		       -- only upwards. Lowering it mid-day would put somebody who has
		       -- already drawn over their own limit, which the table's CHECK
		       -- would refuse — turning a policy change into a failed request
		       -- for a person who did nothing wrong.
		       granted_mnt = GREATEST(petro_entitlements.granted_mnt, EXCLUDED.granted_mnt),
		       updated_at = NOW()
		 WHERE petro_entitlements.used_mnt + $4 <= petro_entitlements.granted_mnt
		RETURNING granted_mnt::float8, used_mnt::float8`,
		citizenID, tenantID, grant, request.AmountMNT).Scan(&granted, &used)
	if errors.Is(err, pgx.ErrNoRows) {
		// The ON CONFLICT clause was refused by its own WHERE: the balance does
		// not cover it. Answering 409 rather than 400 — the request was well
		// formed and would succeed tomorrow.
		nexus.Error(w, http.StatusConflict, "өнөөдрийн эрх хүрэлцэхгүй байна")
		return
	}
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not draw on the entitlement")
		return
	}

	token, err := voucherToken()
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not mint the voucher")
		return
	}

	var (
		voucher     Voucher
		stationID   *string
		stationName *string
	)
	if request.IntendedStationID != "" {
		stationID = &request.IntendedStationID
	}

	err = tx.QueryRow(r.Context(), `
		WITH inserted AS (
		    INSERT INTO petro_vouchers
		           (citizen_id, tenant_id, for_date, amount_mnt, fuel_type, fuel_label,
		            intended_station_id, qr_token, expires_at)
		    SELECT $1, $2, CURRENT_DATE, $3, $4, $8,
		           $5::uuid, $6, NOW() + $7::interval
		    RETURNING *
		)
		SELECT i.id::text, i.amount_mnt::float8, i.fuel_type, i.fuel_label,
		       i.qr_token, i.status, i.expires_at, i.created_at, s.name
		  FROM inserted i
		  LEFT JOIN petro_stations s ON s.id = i.intended_station_id`,
		citizenID, tenantID, request.AmountMNT, request.FuelType,
		stationID, token, VoucherLifetime.String(), fuelLabel(request.FuelType)).
		Scan(&voucher.ID, &voucher.AmountMNT, &voucher.FuelType, &voucher.FuelLabel,
			&voucher.QRToken, &voucher.Status, &voucher.ExpiresAt, &voucher.CreatedAt, &stationName)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not mint the voucher")
		return
	}
	if stationName != nil {
		voucher.StationName = *stationName
	}

	if err := tx.Commit(r.Context()); err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not save the voucher")
		return
	}

	nexus.Audit(r.Context(), tenantID, citizenID, "petro.voucher.issued", voucher.ID,
		map[string]any{"amount_mnt": voucher.AmountMNT, "fuel_type": voucher.FuelType})

	nexus.JSON(w, http.StatusCreated, map[string]any{
		"voucher": voucher,
		"entitlement": Entitlement{
			Date:        nexus.Today().Format("2006-01-02"),
			GrantedMNT:  granted,
			UsedMNT:     used,
			RemainigMNT: granted - used,
		},
	})
}

// handleMyVouchers lists this citizen's vouchers for today.
func (m *Module) handleMyVouchers(w http.ResponseWriter, r *http.Request) {
	citizenID, _, err := citizen(r)
	if err != nil {
		nexus.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	rows, err := m.db.Query(r.Context(), `
		SELECT v.id::text, v.amount_mnt::float8, v.fuel_type, v.fuel_label,
		       v.qr_token, v.status, v.expires_at, v.created_at,
		       COALESCE(s.name, ''), v.redeemed_at
		  FROM petro_vouchers v
		  LEFT JOIN petro_stations s ON s.id = v.intended_station_id
		 WHERE v.citizen_id = $1 AND v.for_date = CURRENT_DATE
		 ORDER BY v.created_at DESC
		 LIMIT 50`, citizenID)
	if err != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not load your vouchers")
		return
	}
	defer rows.Close()

	vouchers := []Voucher{}
	for rows.Next() {
		var v Voucher
		if err := rows.Scan(&v.ID, &v.AmountMNT, &v.FuelType, &v.FuelLabel,
			&v.QRToken, &v.Status, &v.ExpiresAt, &v.CreatedAt,
			&v.StationName, &v.RedeemedAt); err != nil {
			nexus.Error(w, http.StatusInternalServerError, "could not read your vouchers")
			return
		}
		// An active voucher whose time has passed is expired, whatever the
		// column says: the sweep that writes that down runs when somebody asks
		// for a new one, and a list read before then must not offer a dead one.
		if v.Status == "active" && time.Now().After(v.ExpiresAt) {
			v.Status = "expired"
		}
		vouchers = append(vouchers, v)
	}
	if rows.Err() != nil {
		nexus.Error(w, http.StatusInternalServerError, "could not read your vouchers")
		return
	}

	nexus.JSON(w, http.StatusOK, map[string]any{"vouchers": vouchers, "count": len(vouchers)})
}

// voucherToken is what the pump scans.
//
// 256 bits from crypto/rand, URL-safe. The system this replaces used six hex
// characters, which is sixteen million possibilities and no rate limit on the
// endpoint that checked them.
func voucherToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// settleExpiredSQL expires a citizen's run-out vouchers and gives back what
// they held, in one statement.
//
// It used to be two: sum the active-but-expired vouchers into the refund, then
// flip them to expired. Two requests at once both summed the same vouchers —
// the second waited on the entitlement row, re-checked only that row, and
// subtracted the stale sum again — so the ration came back twice. Refunding
// exactly the rows this statement itself flipped (RETURNING) makes the second
// request find nothing left to refund.
const settleExpiredSQL = `
	WITH expired AS (
	    UPDATE petro_vouchers SET status = 'expired'
	     WHERE citizen_id = $1 AND status = 'active' AND expires_at <= NOW()
	    RETURNING for_date, amount_mnt
	)
	UPDATE petro_entitlements e
	   SET used_mnt = GREATEST(e.used_mnt - x.amount, 0), updated_at = NOW()
	  FROM (SELECT for_date, SUM(amount_mnt) AS amount FROM expired GROUP BY for_date) AS x
	 WHERE e.citizen_id = $1 AND e.for_date = x.for_date`

// settleExpiredVouchers gives back what an unspent voucher held.
//
// Lifted out of the issue path so the read can run it too — the two callers
// share settleExpiredSQL, and a copy would be a second definition of what
// "expired" means to an entitlement.
func (m *Module) settleExpiredVouchers(ctx context.Context, citizenID string) error {
	_, err := m.db.Exec(ctx, settleExpiredSQL, citizenID)
	return err
}
