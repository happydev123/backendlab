package config

import (
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/redact"
	"github.com/happydev123/backendlab/internal/yamlutil"
)

// hostPattern matches a DNS host name (letters, digits, hyphens, dots).
var hostPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*\.?$`)

// Server is the configuration for "backendlab serve" and "backendlab
// worker". Targets, credential references and write permission are
// controlled here; submitted runs can only choose among them.
type Server struct {
	Version     int               `yaml:"version"`
	Listen      string            `yaml:"listen"`
	Database    Database          `yaml:"database"`
	SuitesDir   string            `yaml:"suites_dir"`
	Targets     map[string]Target `yaml:"targets"`
	Credentials Credentials       `yaml:"credentials"`
	Egress      Egress            `yaml:"egress"`
	// AllowWrites permits suites with mutating cases. A submitted run must
	// also request writes explicitly; neither alone is enough.
	AllowWrites bool          `yaml:"allow_writes"`
	Worker      Worker        `yaml:"worker"`
	Auth        Auth          `yaml:"auth"`
	Retention   Retention     `yaml:"retention"`
	Limits      limits.Limits `yaml:"limits"`
	Redact      redact.Policy `yaml:"redact"`
	Log         Log           `yaml:"log"`
}

// Database holds connection pool settings. The URL itself is a secret and
// is read from the environment variable named by URLEnv.
type Database struct {
	URLEnv          string        `yaml:"url_env"`
	MaxConns        int32         `yaml:"max_conns"`
	MinConns        int32         `yaml:"min_conns"`
	MaxConnLifetime time.Duration `yaml:"max_conn_lifetime"`
	MaxConnIdleTime time.Duration `yaml:"max_conn_idle_time"`
}

// Credentials lists the environment variables target headers may read.
type Credentials struct {
	AllowedEnv []string `yaml:"allowed_env"`
}

// Egress restricts outbound destinations for server-executed runs.
// Application checks complement, but do not replace, network-level egress
// controls.
type Egress struct {
	// AllowHosts lists permitted hosts ("api.example.com",
	// "*.internal.example.com", optionally with ":port").
	AllowHosts []string `yaml:"allow_hosts"`
	// AllowCIDRs permits private address ranges explicitly, for trusted
	// operators running against internal upstreams.
	AllowCIDRs []string `yaml:"allow_cidrs"`
	// AllowLoopback permits 127.0.0.0/8 and ::1 (development only).
	AllowLoopback bool `yaml:"allow_loopback"`
}

// Worker configures background execution.
type Worker struct {
	Concurrency         int           `yaml:"concurrency"`
	LeaseDuration       time.Duration `yaml:"lease_duration"`
	HeartbeatInterval   time.Duration `yaml:"heartbeat_interval"`
	PollInterval        time.Duration `yaml:"poll_interval"`
	MaxReadOnlyAttempts int           `yaml:"max_read_only_attempts"`
}

// Auth configures operator login.
type Auth struct {
	PasswordHashEnv  string        `yaml:"password_hash_env"`
	SessionTTL       time.Duration `yaml:"session_ttl"`
	SecureCookies    *bool         `yaml:"secure_cookies"`
	LoginMaxAttempts int           `yaml:"login_max_attempts"`
	LoginWindow      time.Duration `yaml:"login_window"`
}

// Retention controls what is stored and for how long.
type Retention struct {
	RunMaxAge time.Duration `yaml:"run_max_age"`
	// RetainRawBodies stores bounded, redacted response bodies with case
	// results. Off by default; enabling it stores upstream data in
	// PostgreSQL and should be paired with database access restrictions.
	RetainRawBodies bool  `yaml:"retain_raw_bodies"`
	RawBodyMaxBytes int64 `yaml:"raw_body_max_bytes"`
}

// Log configures structured logging.
type Log struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// SecureCookiesEnabled reports the effective cookie setting (default on).
func (a Auth) SecureCookiesEnabled() bool { return a.SecureCookies == nil || *a.SecureCookies }

// LoadServer reads and validates a server configuration file.
func LoadServer(path string) (*Server, error) {
	data, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	return ParseServer(data)
}

// ParseServer decodes, defaults and validates server configuration.
func ParseServer(data []byte) (*Server, error) {
	var s Server
	if err := yamlutil.DecodeStrict(data, &s); err != nil {
		return nil, fmt.Errorf("server config: %w", err)
	}
	s.applyDefaults()
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("server config: %w", err)
	}
	return &s, nil
}

func (s *Server) applyDefaults() {
	if s.Listen == "" {
		s.Listen = "127.0.0.1:8080"
	}
	if s.Database.URLEnv == "" {
		s.Database.URLEnv = "BACKENDLAB_DATABASE_URL"
	}
	if s.Database.MaxConns == 0 {
		s.Database.MaxConns = 10
	}
	if s.Database.MaxConnLifetime == 0 {
		s.Database.MaxConnLifetime = time.Hour
	}
	if s.Database.MaxConnIdleTime == 0 {
		s.Database.MaxConnIdleTime = 10 * time.Minute
	}
	if s.Worker.Concurrency == 0 {
		s.Worker.Concurrency = 2
	}
	if s.Worker.LeaseDuration == 0 {
		s.Worker.LeaseDuration = 30 * time.Second
	}
	if s.Worker.HeartbeatInterval == 0 {
		s.Worker.HeartbeatInterval = 10 * time.Second
	}
	if s.Worker.PollInterval == 0 {
		s.Worker.PollInterval = 2 * time.Second
	}
	if s.Worker.MaxReadOnlyAttempts == 0 {
		s.Worker.MaxReadOnlyAttempts = 2
	}
	if s.Auth.PasswordHashEnv == "" {
		s.Auth.PasswordHashEnv = "BACKENDLAB_ADMIN_PASSWORD_HASH"
	}
	if s.Auth.SessionTTL == 0 {
		s.Auth.SessionTTL = 12 * time.Hour
	}
	if s.Auth.LoginMaxAttempts == 0 {
		s.Auth.LoginMaxAttempts = 5
	}
	if s.Auth.LoginWindow == 0 {
		s.Auth.LoginWindow = 15 * time.Minute
	}
	if s.Retention.RunMaxAge == 0 {
		s.Retention.RunMaxAge = 30 * 24 * time.Hour
	}
	if s.Retention.RawBodyMaxBytes == 0 {
		s.Retention.RawBodyMaxBytes = 64 << 10
	}
	if s.Log.Level == "" {
		s.Log.Level = "info"
	}
	if s.Log.Format == "" {
		s.Log.Format = "json"
	}
	if s.Targets == nil {
		s.Targets = map[string]Target{}
	}
	s.Limits = s.Limits.Merge()
}

// Validate checks the defaulted configuration.
func (s *Server) Validate() error {
	if s.Version != 1 {
		return fmt.Errorf("version must be 1 (got %d)", s.Version)
	}
	if _, _, err := net.SplitHostPort(s.Listen); err != nil {
		return fmt.Errorf("listen %q: %v", s.Listen, err)
	}
	if !envNamePattern.MatchString(s.Database.URLEnv) {
		return fmt.Errorf("database.url_env %q is not a valid variable name", s.Database.URLEnv)
	}
	if s.Database.MaxConns < 1 || s.Database.MaxConns > 100 {
		return fmt.Errorf("database.max_conns must be between 1 and 100")
	}
	if s.Database.MinConns < 0 || s.Database.MinConns > s.Database.MaxConns {
		return fmt.Errorf("database.min_conns must be between 0 and max_conns")
	}
	if strings.TrimSpace(s.SuitesDir) == "" {
		return fmt.Errorf("suites_dir is required (approved suites are loaded only from it)")
	}
	allowedEnv := map[string]bool{}
	for _, e := range s.Credentials.AllowedEnv {
		if !envNamePattern.MatchString(e) {
			return fmt.Errorf("credentials.allowed_env: invalid variable name %q", e)
		}
		if e == s.Database.URLEnv || e == s.Auth.PasswordHashEnv {
			return fmt.Errorf("credentials.allowed_env must not include %s", e)
		}
		allowedEnv[e] = true
	}
	if len(s.Targets) == 0 {
		return fmt.Errorf("at least one target is required")
	}
	for _, name := range sortedKeys(s.Targets) {
		t := s.Targets[name]
		if err := t.Validate(name); err != nil {
			return err
		}
		for _, h := range sortedKeys(t.Headers) {
			if env := t.Headers[h].Env; env != "" && !allowedEnv[env] {
				return fmt.Errorf("target %s: header %s reads %s, which is not in credentials.allowed_env", name, h, env)
			}
		}
		if t.CAFile != "" {
			return fmt.Errorf("target %s: ca_file is not supported in server mode; install the CA in the system trust store", name)
		}
	}
	if err := s.Egress.Validate(); err != nil {
		return err
	}
	w := s.Worker
	switch {
	case w.Concurrency < 1 || w.Concurrency > 32:
		return fmt.Errorf("worker.concurrency must be between 1 and 32")
	case w.LeaseDuration < 5*time.Second:
		return fmt.Errorf("worker.lease_duration must be at least 5s")
	case w.HeartbeatInterval <= 0 || w.HeartbeatInterval*2 > w.LeaseDuration:
		return fmt.Errorf("worker.heartbeat_interval must be positive and at most half of lease_duration")
	case w.PollInterval <= 0:
		return fmt.Errorf("worker.poll_interval must be positive")
	case w.MaxReadOnlyAttempts < 1 || w.MaxReadOnlyAttempts > 5:
		return fmt.Errorf("worker.max_read_only_attempts must be between 1 and 5")
	}
	if !envNamePattern.MatchString(s.Auth.PasswordHashEnv) {
		return fmt.Errorf("auth.password_hash_env %q is not a valid variable name", s.Auth.PasswordHashEnv)
	}
	if s.Auth.SessionTTL < time.Minute || s.Auth.SessionTTL > 7*24*time.Hour {
		return fmt.Errorf("auth.session_ttl must be between 1m and 168h")
	}
	if s.Auth.LoginMaxAttempts < 1 || s.Auth.LoginWindow <= 0 {
		return fmt.Errorf("auth.login_max_attempts and auth.login_window must be positive")
	}
	if s.Retention.RunMaxAge < time.Hour {
		return fmt.Errorf("retention.run_max_age must be at least 1h")
	}
	if s.Retention.RawBodyMaxBytes < 0 || s.Retention.RawBodyMaxBytes > 1<<20 {
		return fmt.Errorf("retention.raw_body_max_bytes must be between 0 and 1048576")
	}
	if err := s.Limits.Validate(); err != nil {
		return err
	}
	if _, err := redact.New(s.Redact); err != nil {
		return err
	}
	switch s.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log.level must be debug, info, warn or error")
	}
	switch s.Log.Format {
	case "json", "text":
	default:
		return fmt.Errorf("log.format must be json or text")
	}
	return nil
}

// AllowedEnv returns the credential allowlist as a set.
func (s *Server) AllowedEnv() map[string]bool {
	m := make(map[string]bool, len(s.Credentials.AllowedEnv))
	for _, e := range s.Credentials.AllowedEnv {
		m[e] = true
	}
	return m
}

// Redactor builds the configured redactor.
func (s *Server) Redactor() *redact.Redactor {
	r, err := redact.New(s.Redact)
	if err != nil {
		return redact.Default()
	}
	return r
}

// Validate checks host patterns and CIDRs.
func (e Egress) Validate() error {
	for _, h := range e.AllowHosts {
		host := h
		if hh, port, err := net.SplitHostPort(h); err == nil {
			host = hh
			if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
				return fmt.Errorf("egress.allow_hosts: %q has an invalid port", h)
			}
		}
		host = strings.TrimPrefix(host, "*.")
		if _, err := netip.ParseAddr(host); err == nil {
			continue
		}
		if !hostPattern.MatchString(host) {
			return fmt.Errorf("egress.allow_hosts: invalid host pattern %q", h)
		}
	}
	for _, c := range e.AllowCIDRs {
		if _, err := netip.ParsePrefix(c); err != nil {
			return fmt.Errorf("egress.allow_cidrs: %q: %v", c, err)
		}
	}
	return nil
}
