package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
)

// maxMigrationBytes bounds a single migration file.
const maxMigrationBytes = 1 << 20

// migrationName matches NNNN_snake_name.up.sql.
var migrationName = regexp.MustCompile(`^([0-9]{4})_([a-z0-9_]+)\.up\.sql$`)

// Migration is one versioned schema file.
type Migration struct {
	Version  int
	Name     string
	Checksum string // hex SHA-256 of the file bytes
	SQL      string
}

// LoadMigrations reads every *.up.sql file at the root of fsys, sorted by
// version. Files that do not match the naming scheme, duplicate versions,
// version 0, empty files and files over 1 MiB are rejected. Other files
// (such as Go sources) are ignored.
func LoadMigrations(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("migrations: %w", err)
	}
	var out []Migration
	seen := map[int]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !isSQLFile(name) {
			continue
		}
		m := migrationName.FindStringSubmatch(name)
		if m == nil {
			return nil, fmt.Errorf("migrations: %s does not match NNNN_name.up.sql", name)
		}
		version, _ := strconv.Atoi(m[1]) // four digits, always valid
		if version == 0 {
			return nil, fmt.Errorf("migrations: %s: version must start at 0001", name)
		}
		if prev, dup := seen[version]; dup {
			return nil, fmt.Errorf("migrations: %s and %s share version %d", prev, name, version)
		}
		seen[version] = name
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("migrations: read %s: %w", name, err)
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("migrations: %s is empty", name)
		}
		if len(data) > maxMigrationBytes {
			return nil, fmt.Errorf("migrations: %s exceeds %d bytes", name, maxMigrationBytes)
		}
		out = append(out, Migration{
			Version:  version,
			Name:     m[2],
			Checksum: checksum(data),
			SQL:      string(data),
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("migrations: no *.up.sql files found")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

func isSQLFile(name string) bool {
	return len(name) > 4 && name[len(name)-4:] == ".sql"
}

func checksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
