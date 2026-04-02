package config

import (
	"strings"
	"testing"
)

const serverDoc = `
version: 1
suites_dir: /etc/backendlab/suites
targets:
  staging:
    base_url: https://staging.example.com
    headers:
      X-Api-Key: {env: STAGING_API_KEY}
credentials:
  allowed_env: [STAGING_API_KEY]
egress:
  allow_hosts: [staging.example.com, "*.internal.example.com:8443"]
  allow_cidrs: [10.20.0.0/16]
`

func TestParseServerDefaults(t *testing.T) {
	s, err := ParseServer([]byte(serverDoc))
	if err != nil {
		t.Fatal(err)
	}
	if s.Listen != "127.0.0.1:8080" || s.Database.URLEnv != "BACKENDLAB_DATABASE_URL" {
		t.Fatalf("defaults = %+v", s)
	}
	if s.Worker.Concurrency != 2 || s.Worker.HeartbeatInterval*2 > s.Worker.LeaseDuration {
		t.Fatalf("worker = %+v", s.Worker)
	}
	if !s.Auth.SecureCookiesEnabled() {
		t.Fatal("secure cookies should default on")
	}
	if s.AllowWrites {
		t.Fatal("writes must default off")
	}
	if !s.AllowedEnv()["STAGING_API_KEY"] {
		t.Fatal("allowed env missing")
	}
}

func TestServerErrors(t *testing.T) {
	tests := map[string]string{
		"credential not allowed": strings.Replace(serverDoc, "allowed_env: [STAGING_API_KEY]", "allowed_env: [OTHER]", 1),
		"db url in allowlist":    strings.Replace(serverDoc, "allowed_env: [STAGING_API_KEY]", "allowed_env: [STAGING_API_KEY, BACKENDLAB_DATABASE_URL]", 1),
		"no suites dir":          strings.Replace(serverDoc, "suites_dir: /etc/backendlab/suites", "", 1),
		"bad cidr":               strings.Replace(serverDoc, "10.20.0.0/16", "10.20.0.0/99", 1),
		"bad host":               strings.Replace(serverDoc, "staging.example.com, ", "'http://x/', ", 1),
		"heartbeat too slow":     serverDoc + "worker: {lease_duration: 10s, heartbeat_interval: 8s}\n",
		"bad log level":          serverDoc + "log: {level: verbose}\n",
		"ca file":                strings.Replace(serverDoc, "    headers:", "    ca_file: /x.pem\n    headers:", 1),
		"bad listen":             serverDoc + "listen: nope\n",
	}
	for name, doc := range tests {
		if _, err := ParseServer([]byte(doc)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
