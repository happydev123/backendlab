// Package version reports the BackendLab build version.
package version

// Version is overridden at build time with
// -ldflags "-X github.com/happydev123/backendlab/internal/version.Version=...".
var Version = "0.1.0-dev"

// Name is the tool name written into reports.
const Name = "backendlab"
