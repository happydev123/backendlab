package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/happydev123/backendlab/internal/auth"
	"github.com/happydev123/backendlab/internal/catalog"
	"github.com/happydev123/backendlab/internal/jobs"
	"github.com/happydev123/backendlab/internal/runner"
	"github.com/happydev123/backendlab/internal/server"
)

const serveUsage = "serve [--config FILE] [--with-worker]"

// serveCommand runs the authenticated API and dashboard. With
// --with-worker it also executes queued runs in the same process, which is
// convenient for a single machine; separate worker processes are the
// default deployment.
func serveCommand(ctx context.Context, app *App, args []string) int {
	fs := newFlagSet("serve", app.Stderr, serveUsage)
	configPath := fs.String("config", "", "server configuration file (or "+EnvServerConfig+")")
	withWorker := fs.Bool("with-worker", false, "also run a background worker in this process")
	pos, err := parseArgs(fs, args)
	if code, done := handleParseError(err); done {
		return code
	}
	if len(pos) != 0 {
		return app.fail(usagef("usage: backendlab %s", serveUsage))
	}
	env, err := app.openServerEnv(ctx, *configPath)
	if err != nil {
		return app.fail(err)
	}
	defer env.db.Close()
	if err := requireMigrated(ctx, env); err != nil {
		return app.fail(err)
	}
	hash, ok := app.lookupEnv(env.cfg.Auth.PasswordHashEnv)
	if !ok || strings.TrimSpace(hash) == "" {
		return app.fail(fmt.Errorf("environment variable %s is not set; generate a hash with 'backendlab hash-password'", env.cfg.Auth.PasswordHashEnv))
	}
	authSvc, err := auth.NewService(env.db, auth.OptionsFromConfig(env.cfg.Auth, strings.TrimSpace(hash)))
	if err != nil {
		return app.fail(err)
	}
	cat, err := catalog.Load(env.cfg)
	if err != nil {
		return app.fail(err)
	}
	for _, p := range cat.Problems() {
		env.log.Warn("suite not approved", "file", p.File, "error", p.Error)
	}
	queue := jobs.NewQueue(env.db, nil)
	srv, err := server.New(server.Deps{
		Config:  env.cfg,
		DB:      env.db,
		Auth:    authSvc,
		Queue:   queue,
		Catalog: cat,
		Logger:  env.log,
	})
	if err != nil {
		return app.fail(err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var workerErr error
	if *withWorker {
		exec, err := runner.NewJobExecutor(env.cfg, app.envLookup())
		if err != nil {
			return app.fail(err)
		}
		w, err := jobs.NewWorker(queue, exec, jobs.ConfigFromServer(jobs.DefaultWorkerID(), env.cfg), env.log)
		if err != nil {
			return app.fail(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := w.Run(ctx); err != nil {
				workerErr = err
				cancel()
			}
		}()
	}
	env.log.Info("server starting", "listen", env.cfg.Listen, "suites", len(cat.List()), "with_worker", *withWorker)
	err = srv.ListenAndServe(ctx)
	cancel()
	wg.Wait()
	if err != nil && !errors.Is(err, context.Canceled) {
		return app.fail(fmt.Errorf("serve: %w", err))
	}
	if workerErr != nil {
		return app.fail(fmt.Errorf("worker: %w", workerErr))
	}
	env.log.Info("server stopped")
	return ExitOK
}
