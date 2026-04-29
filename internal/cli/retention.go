package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/happydev123/backendlab/migrations"
)

const retentionUsage = "retention cleanup [--config FILE] [--dry-run]"

// retentionCommand deletes finished runs (and their case results) older
// than retention.run_max_age, plus expired sessions and stale login
// attempts. Queued and running runs are never deleted.
func retentionCommand(ctx context.Context, app *App, args []string) int {
	if len(args) == 0 || args[0] != "cleanup" {
		return app.fail(usagef("usage: backendlab %s", retentionUsage))
	}
	fs := newFlagSet("retention cleanup", app.Stderr, retentionUsage)
	configPath := fs.String("config", "", "server configuration file (or "+EnvServerConfig+")")
	dryRun := fs.Bool("dry-run", false, "print the cutoff without deleting anything")
	pos, err := parseArgs(fs, args[1:])
	if code, done := handleParseError(err); done {
		return code
	}
	if len(pos) != 0 {
		return app.fail(usagef("usage: backendlab %s", retentionUsage))
	}
	env, err := app.openServerEnv(ctx, *configPath)
	if err != nil {
		return app.fail(err)
	}
	defer env.db.Close()
	if err := requireMigrated(ctx, env); err != nil {
		return app.fail(err)
	}
	cutoff := time.Now().Add(-env.cfg.Retention.RunMaxAge).UTC()
	if *dryRun {
		fmt.Fprintf(app.Stdout, "would delete finished runs that ended before %s\n", cutoff.Format(time.RFC3339))
		return ExitOK
	}
	runs, err := env.db.RetentionCleanup(ctx, cutoff)
	if err != nil {
		return app.fail(err)
	}
	sessions, err := env.db.DeleteExpiredSessions(ctx, time.Now())
	if err != nil {
		return app.fail(err)
	}
	attempts, err := env.db.DeleteLoginAttemptsBefore(ctx, time.Now().Add(-env.cfg.Auth.LoginWindow))
	if err != nil {
		return app.fail(err)
	}
	fmt.Fprintf(app.Stdout, "deleted %d finished runs older than %s, %d expired sessions, %d old login attempts\n",
		runs, cutoff.Format(time.RFC3339), sessions, attempts)
	return ExitOK
}

// requireMigrated refuses to start when migrations are pending or were
// modified, so a process never runs against an unexpected schema.
func requireMigrated(ctx context.Context, env *serverEnv) error {
	states, err := env.db.MigrationStatus(ctx, migrations.FS)
	if err != nil {
		return err
	}
	for _, s := range states {
		if s.Modified || s.Unknown || !s.Applied {
			return fmt.Errorf("database schema is not current (migration %04d %s); run 'backendlab migrate' first", s.Version, s.Name)
		}
	}
	return nil
}
