/*
 * Gerege Nexus
 * Copyright (c) 2026 Gerege Systems Development Team, Gerege Nomadica Foundation
 * Distributed under the Apache 2.0 License.
 *
 * Two things that must happen whether or not anybody opens a screen.
 *
 *	the periods exist        — a window nobody opened is a window nobody answers
 *	the day is computed      — the dashboard has three seconds, not a query
 *
 * # Why a ticker and not a scheduler
 *
 * The platform's scheduler runs reports, and both jobs here are writes rather
 * than reports. A ticker in the module is twenty lines; a scheduling subsystem
 * is a thing to operate. The exchange is that this runs on every replica
 * instead of on one — which is safe here and only here, because both statements
 * are idempotent: the periods insert with ON CONFLICT DO NOTHING and the day
 * upserts. Two replicas doing this at once produce the same rows as one.
 *
 * ponytail: per-replica ticker, no leader election — revisit if a job appears
 * that is not idempotent.
 *
 * # Why the first pass waits
 *
 * The platform applies a module's migrations after it constructs the module,
 * so at New() these tables do not exist yet — the first pass ran into three
 * "relation does not exist" errors on every boot and then sat out the hour
 * until the first tick. It now retries on a widening delay until one pass
 * succeeds, and only then settles into the hourly cadence. Found by reading
 * the logs of the first real deployment, which is the only place the ordering
 * is observable.
 *
 * # Why the last three days and not only today
 *
 * A report may arrive late, and a day whose figures changed after it was
 * computed would keep the old number until somebody noticed. Three days covers
 * the grace period the policy allows, and costs three small statements an hour.
 */

package petro

import (
	"context"
	"log/slog"
	"time"

	"github.com/gerege-systems/open-gerege-nexus/backend/pkg/nexus"
)

// jobInterval is how often the two statements run.
//
// Hourly rather than nightly because the day being computed is today: an
// official opening the dashboard at four in the afternoon should see the
// morning's submissions, not yesterday's total.
const jobInterval = time.Hour

// Start is nexus.Starter: the platform calls it once the server is assembled,
// with a context it cancels on graceful shutdown, so the ticker stops with the
// process instead of being cut off when the pool closes under it. It used to
// be started from New with context.Background(), which nothing ever cancelled.
func (m *Module) Start(ctx context.Context) { m.StartJobs(ctx) }

// A core that stops calling Start would leave the jobs silently unstarted;
// this turns that into a compile error instead.
var _ nexus.Starter = (*Module)(nil)

// StartJobs runs the module's periodic work until ctx is done.
//
// The first pass runs after a short delay rather than on the hour, so a
// deployment that boots at midnight has today's period before anybody tries to
// answer it.
func (m *Module) StartJobs(ctx context.Context) {
	go func() {
		// 15s, 30s, 60s … up to eight minutes. Long enough to outlast a slow
		// migration, short enough that a deployment has its reporting period
		// before anybody arrives to answer it.
		for attempt := 0; attempt < 6; attempt++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(1<<attempt) * 15 * time.Second):
			}
			if m.runJobs(ctx) {
				break
			}
		}

		ticker := time.NewTicker(jobInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.runJobs(ctx)
			}
		}
	}()
}

// runJobs answers whether the pass got through without an error, which is what
// the startup retry above waits for.
func (m *Module) runJobs(ctx context.Context) (ok bool) {
	// A panic on this goroutine takes the whole API process down: it is not
	// inside a request, so nothing above it recovers. A missed pass is a gap
	// in a table until the next tick; a crash is every request on the
	// deployment, and the two are not close (audit §31).
	defer func() {
		if reason := recover(); reason != nil {
			slog.Error("petro: scheduled work panicked", "reason", reason)
			ok = false
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	policy := m.LoadPolicy(ctx)
	ok = true

	// nexus.Now(), not time.Now(): a reporting period is a calendar day, and
	// the calendar this system keeps is Ulaanbaatar's. In UTC the period for
	// "today" opened at eight in the morning.
	if err := m.EnsurePeriods(ctx, nexus.Now(), policy); err != nil {
		slog.Error("petro: could not open the reporting periods", "error", err)
		ok = false
	}

	for offset := 0; offset < 3; offset++ {
		day := nexus.Now().AddDate(0, 0, -offset)
		if err := m.RefreshDaily(ctx, day); err != nil {
			slog.Error("petro: could not refresh the national table",
				"day", day.Format("2006-01-02"), "error", err)
			ok = false
		}
	}

	return ok
}
