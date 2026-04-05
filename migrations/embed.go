// Package migrations embeds the versioned PostgreSQL schema used by the
// JobDock store. Files are named NNNN_name.up.sql, applied in version order
// by store.Migrate, and never edited after release: the runner records a
// SHA-256 checksum of each applied file and refuses to continue if an
// applied file changes. Migrations are forward-only; there are no down
// files.
package migrations

import "embed"

// FS holds every migration file.
//
//go:embed *.up.sql
var FS embed.FS
