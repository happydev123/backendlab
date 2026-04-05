package store

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/happydev123/backendlab/migrations"
)

func TestLoadMigrationsSorted(t *testing.T) {
	fsys := fstest.MapFS{
		"0002_second.up.sql": {Data: []byte("SELECT 2;")},
		"0001_first.up.sql":  {Data: []byte("SELECT 1;")},
		"embed.go":           {Data: []byte("package migrations")},
	}
	ms, err := LoadMigrations(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].Version != 1 || ms[1].Version != 2 {
		t.Fatalf("got %+v", ms)
	}
	if ms[0].Name != "first" || ms[0].SQL != "SELECT 1;" {
		t.Errorf("first = %+v", ms[0])
	}
	if len(ms[0].Checksum) != 64 || ms[0].Checksum == ms[1].Checksum {
		t.Errorf("checksums %q %q", ms[0].Checksum, ms[1].Checksum)
	}
}

func TestLoadMigrationsRejects(t *testing.T) {
	cases := map[string]fstest.MapFS{
		"bad name":  {"1_x.up.sql": {Data: []byte("x")}},
		"down file": {"0001_x.down.sql": {Data: []byte("x")}},
		"duplicate": {"0001_a.up.sql": {Data: []byte("x")}, "0001_b.up.sql": {Data: []byte("y")}},
		"zero":      {"0000_a.up.sql": {Data: []byte("x")}},
		"empty":     {"0001_a.up.sql": {Data: nil}},
		"none":      {"README": {Data: []byte("x")}},
		"too large": {"0001_a.up.sql": {Data: []byte(strings.Repeat("-", maxMigrationBytes+1))}},
	}
	for name, fsys := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadMigrations(fsys); err == nil {
				t.Error("accepted")
			}
		})
	}
}

func TestEmbeddedMigrationsLoad(t *testing.T) {
	ms, err := LoadMigrations(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range ms {
		if m.Version != i+1 {
			t.Errorf("embedded migration %d has version %d; versions must be contiguous", i, m.Version)
		}
	}
}
