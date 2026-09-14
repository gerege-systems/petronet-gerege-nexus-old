/*
 * PetroNet
 * Copyright (c) 2026 Gerege Systems Development Team, Gerege Nomadica Foundation.
 * Distributed under the Apache 2.0 License.
 */

// Command petronet runs the Gerege Nexus platform as the PetroNet
// distribution: the national fuel reserve's monitoring and management system,
// at petronet.mn.
//
// There is no core code in this repository — go.mod's one line is the whole of
// it. This product's own apps live under modules/, and the repository is
// Level 2 precisely so that adding one is a change to this file rather than a
// migration of the deployment (docs/ECOSYSTEM_GIT_STRATEGY.md, §1).
//
// It identifies people itself: no SSO_CLIENT_ISSUER, its own sign-in, its own
// database. That is a deployment decision and nothing in this file knows about
// it — see deploy/docker-compose.yml.
//
// It carries one module, petro: the registry of depots and stations, the
// chain of custody from import to nozzle, the reporting periods the regulator
// reads, and the citizen entitlements on top of them. Modules go in the
// Options.Modules callback and nowhere else — logic written in this file
// instead of in a module is logic no other deployment can have and no test can
// reach.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/gerege-systems/open-gerege-nexus/backend/pkg/host"
	"github.com/gerege-systems/open-gerege-nexus/backend/pkg/nexus"
	"github.com/gerege-systems/petronet-gerege-nexus/modules/petro"
	"github.com/jackc/pgx/v5"
)

func main() {
	if url := os.Getenv("DATABASE_URL"); url != "" {
		if err := waitForDatabase(url, time.Minute); err != nil {
			slog.Error("petronet did not start", "error", err)
			os.Exit(1)
		}
	}

	// The error is checked and the exit code is the point: a distribution that
	// cannot start must not exit 0 and read as a clean shutdown to whatever is
	// supervising it.
	if err := host.Run(host.Options{
		Modules: func(p nexus.Platform) {
			petro.New(p)
		},
		// PetroNet is the fuel-sector distribution, so the fuel network is part
		// of every organisation rather than an optional store install.
		DefaultApps: []string{petro.ID},
	}); err != nil {
		slog.Error("petronet stopped", "error", err)
		os.Exit(1)
	}
}

// waitForDatabase holds the start until Postgres accepts a connection.
//
// After a reboot the Docker daemon brings containers back by their restart
// policy and ignores compose's depends_on, so this process can start in the
// same instant as the database. The host then carries on without it and never
// retries installing the catalogue or applying the module's migrations, so a
// migration shipped since the last start stays unapplied and its routes fail
// until somebody restarts the backend. Past the limit the process exits and
// the restart policy tries again.
func waitForDatabase(url string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		conn, err := pgx.Connect(ctx, url)
		if err == nil {
			err = conn.Close(ctx)
		}
		cancel()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("database unreachable after %s: %w", limit, err)
		}
		time.Sleep(time.Second)
	}
}
