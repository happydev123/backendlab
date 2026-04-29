package cli

import (
	"context"
	"fmt"

	"github.com/happydev123/backendlab/internal/jobs"
	"github.com/happydev123/backendlab/internal/runner"
)

const workerUsage = "worker [--config FILE] [--id ID]"

// workerCommand claims queued runs and executes them until interrupted.
// Shutdown is graceful: no new runs are claimed, in-flight runs are given
// a grace period and then released according to the recovery policy.
func workerCommand(ctx context.Context, app *App, args []string) int {
	fs := newFlagSet("worker", app.Stderr, workerUsage)
	configPath := fs.String("config", "", "server configuration file (or "+EnvServerConfig+")")
	id := fs.String("id", "", "worker identifier recorded as lease owner (default host-pid)")
	pos, err := parseArgs(fs, args)
	if code, done := handleParseError(err); done {
		return code
	}
	if len(pos) != 0 {
		return app.fail(usagef("usage: backendlab %s", workerUsage))
	}
	env, err := app.openServerEnv(ctx, *configPath)
	if err != nil {
		return app.fail(err)
	}
	defer env.db.Close()
	if err := requireMigrated(ctx, env); err != nil {
		return app.fail(err)
	}
	exec, err := runner.NewJobExecutor(env.cfg, app.envLookup())
	if err != nil {
		return app.fail(err)
	}
	workerID := *id
	if workerID == "" {
		workerID = jobs.DefaultWorkerID()
	}
	q := jobs.NewQueue(env.db, nil)
	w, err := jobs.NewWorker(q, exec, jobs.ConfigFromServer(workerID, env.cfg), env.log)
	if err != nil {
		return app.fail(err)
	}
	env.log.Info("worker starting", "worker_id", workerID, "concurrency", env.cfg.Worker.Concurrency,
		"allow_writes", env.cfg.AllowWrites, "host", hostname())
	if err := w.Run(ctx); err != nil {
		return app.fail(fmt.Errorf("worker: %w", err))
	}
	env.log.Info("worker stopped", "worker_id", workerID)
	return ExitOK
}
