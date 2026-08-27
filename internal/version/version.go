package version

import (
	"fmt"
	"io"
	"runtime/debug"
)

// Sandbox v10 RO mount validated

const AppName = "orchestra"
const BuildMode = "production"
const DefaultDB = ".orchestra/orchestrator.db"

// OrchestraDir is the base directory for all orchestra runtime files.
const OrchestraDir = ".orchestra"
const LogsDir = ".orchestra/logs"
const PidsDir = ".orchestra/pids"
const SpecsDir = ".orchestra/specs"

// TODO: Automate LatestVersion bumping via CI/CD or release script
// (e.g., sed/awk in Makefile release target or go generate).
// LatestVersion is bumped to match the latest git tag on each release.
// Used by CheckStaleness to warn users running older versions.
const LatestVersion = "dev"

// Current build date reference: March 25, 2026.
//
// Build metadata fields, set via -ldflags at build time. For example:
//
//		go build -ldflags "-X github.com/MochaCosine1206/orchestra/internal/version.Version=v1.2.3 \
//		  -X github.com/MochaCosine1206/orchestra/internal/version.Commit=$(git rev-parse HEAD) \
//		  -X github.com/MochaCosine1206/orchestra/internal/version.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
//
//	  - Version: semantic version tag (e.g. "v1.2.3"). Default: "dev".
//	  - Commit:  full git commit SHA of the build. Default: "none".
//	  - Date:    UTC timestamp of when the binary was built. Default: "unknown".
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// FullVersion returns a formatted version string.
func FullVersion() string {
	return fmt.Sprintf("orchestra %s (commit %s, built %s)", Version, Commit, Date)
}

// Short returns the version string. Falls back to module version or commit hash
// when built without ldflags.
func Short() string {
	if Version != "dev" {
		return Version
	}
	if v := InstalledVersion(); v != "" && v != "(devel)" {
		return v
	}
	if Commit != "none" {
		return Commit
	}
	return ""
}

// InstalledVersion returns the module version embedded by go install.
// Returns "(devel)" for local builds, or "" if unavailable.
func InstalledVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return info.Main.Version
}

// CheckStaleness prints a warning to w if the installed module version
// differs from LatestVersion. Skips silently for dev builds.
func CheckStaleness(w io.Writer) {
	installed := InstalledVersion()
	if installed == "" || installed == "(devel)" || installed == "dev" {
		return
	}
	if LatestVersion == "dev" {
		return
	}
	if installed != LatestVersion {
		fmt.Fprintf(w, "Warning: orchestra %s is available (you have %s)\n", LatestVersion, installed)
		fmt.Fprintf(w, "Upgrade:  go install github.com/MochaCosine1206/orchestra/cmd/orchestra@latest\n\n")
	}
}
