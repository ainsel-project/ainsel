// Package version exposes build-time version information. The variables are
// overridden at link time via -ldflags by the release build.
package version

var (
	// Version is the release version, stamped at link time (default "dev").
	Version = "dev"
	// Commit is the git commit the binary was built from, stamped at link time.
	Commit = "unknown"
	// BuildDate is when the binary was built, stamped at link time.
	BuildDate = "unknown"
)
