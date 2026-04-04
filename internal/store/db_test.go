package store

import (
	"strings"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/config"
)

func TestParsePoolConfigAppliesSettings(t *testing.T) {
	pc, err := ParsePoolConfig("postgres://u:pw@127.0.0.1:5432/db?sslmode=disable", config.Database{
		MaxConns:        7,
		MinConns:        2,
		MaxConnLifetime: 3 * time.Minute,
		MaxConnIdleTime: 45 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if pc.MaxConns != 7 || pc.MinConns != 2 {
		t.Errorf("conns = %d/%d, want 7/2", pc.MaxConns, pc.MinConns)
	}
	if pc.MaxConnLifetime != 3*time.Minute || pc.MaxConnIdleTime != 45*time.Second {
		t.Errorf("lifetimes = %v/%v", pc.MaxConnLifetime, pc.MaxConnIdleTime)
	}
}

func TestParsePoolConfigKeepsDefaultsForZero(t *testing.T) {
	pc, err := ParsePoolConfig("postgres://u@127.0.0.1/db?pool_max_conns=9", config.Database{})
	if err != nil {
		t.Fatal(err)
	}
	if pc.MaxConns != 9 {
		t.Errorf("MaxConns = %d, want 9 from the URL", pc.MaxConns)
	}
}

func TestParsePoolConfigErrors(t *testing.T) {
	if _, err := ParsePoolConfig("  ", config.Database{}); err == nil {
		t.Error("empty URL accepted")
	}
	if _, err := ParsePoolConfig("postgres://u@h/db", config.Database{MaxConns: 2, MinConns: 3}); err == nil {
		t.Error("min_conns > max_conns accepted")
	}
	_, err := ParsePoolConfig("postgres://user:hunter2-secret@127.0.0.1:notaport/db", config.Database{})
	if err == nil {
		t.Fatal("invalid URL accepted")
	}
	if strings.Contains(err.Error(), "hunter2-secret") {
		t.Errorf("error leaks the password: %v", err)
	}
}
