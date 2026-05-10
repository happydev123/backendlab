package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// handleHealthz serves GET /healthz: liveness only. It does not touch the
// database, so a database outage does not get the process restarted.
func (a *API) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz serves GET /readyz: 200 when the database answers a ping
// within the readiness timeout and every migration in the binary is
// applied unmodified; 503 otherwise.
func (a *API) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if e := a.Ready(r.Context()); e != nil {
		WriteError(w, e)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// Ready runs the readiness checks. Details of database errors are logged,
// not returned, because they can name hosts and users.
func (a *API) Ready(ctx context.Context) *Error {
	ctx, cancel := context.WithTimeout(ctx, a.limits.ReadyTimeout)
	defer cancel()
	notReady := func(format string, args ...any) *Error {
		return Errorf(http.StatusServiceUnavailable, CodeNotReady, format, args...)
	}
	if err := a.store.Ping(ctx); err != nil {
		a.logFor(ctx).Warn("readiness: database ping failed", "error", a.cfg.Redactor().Error(err))
		return notReady("database is not reachable")
	}
	states, err := a.store.MigrationStatus(ctx, a.migrations)
	if err != nil {
		a.logFor(ctx).Warn("readiness: migration status failed", "error", a.cfg.Redactor().Error(err))
		return notReady("migration status could not be read")
	}
	var pending, modified, unknown []string
	for _, s := range states {
		name := fmt.Sprintf("%04d_%s", s.Version, s.Name)
		switch {
		case s.Unknown:
			unknown = append(unknown, name)
		case s.Modified:
			modified = append(modified, name)
		case s.Pending():
			pending = append(pending, name)
		}
	}
	switch {
	case len(modified) > 0:
		return notReady("applied migrations were modified: %s", strings.Join(modified, ", "))
	case len(unknown) > 0:
		return notReady("database has migrations this binary does not know (newer schema): %s", strings.Join(unknown, ", "))
	case len(pending) > 0:
		return notReady("%d pending migrations; run backendlab migrate", len(pending))
	}
	return nil
}
