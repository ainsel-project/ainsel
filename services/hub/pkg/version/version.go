// Package version exposes build-time version information. The variables are
// overridden at link time via -ldflags by the release build.
package version

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)
