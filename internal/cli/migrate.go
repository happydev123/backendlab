package cli

import (
	"context"
	"fmt"

	"github.com/happydev123/backendlab/migrations"
)

const migrateUsage = "migrate [status] [--config FILE]"

// migrateCommand applies pending migrations, or lists their state with
// "migrate status". Migrations are never applied implicitly by serve or
// worker; this command is the explicit step.
func migrateCommand(ctx context.Context, app *App, args []string) int {
	fs := newFlagSet("migrate", app.Stderr, migrateUsage)
	configPath := fs.String("config", "", "server configuration file (or "+EnvServerConfig+")")
	pos, err := parseArgs(fs, args)
	if code, done := handleParseError(err); done {
		return code
	}
	statusOnly := false
	switch {
	case len(pos) == 1 && pos[0] == "status":
		statusOnly = true
	case len(pos) > 0:
		return app.fail(usagef("usage: backendlab %s", migrateUsage))
	}
	env, err := app.openServerEnv(ctx, *configPath)
	if err != nil {
		return app.fail(err)
	}
	defer env.db.Close()

	if statusOnly {
		states, err := env.db.MigrationStatus(ctx, migrations.FS)
		if err != nil {
			return app.fail(err)
		}
		problems := false
		for _, s := range states {
			state := "pending"
			switch {
			case s.Unknown:
				state, problems = "unknown (applied, no file)", true
			case s.Modified:
				state, problems = "MODIFIED since applied", true
			case s.Applied:
				state = "applied"
			}
			fmt.Fprintf(app.Stdout, "%04d %-36s %s\n", s.Version, s.Name, state)
		}
		if problems {
			return ExitFailed
		}
		return ExitOK
	}
	applied, err := env.db.Migrate(ctx, migrations.FS)
	if err != nil {
		return app.fail(err)
	}
	if len(applied) == 0 {
		fmt.Fprintln(app.Stdout, "database schema is up to date")
	}
	for _, m := range applied {
		fmt.Fprintf(app.Stdout, "applied %04d %s\n", m.Version, m.Name)
	}
	return ExitOK
}
