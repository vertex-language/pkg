// Package provider defines the shared contract every native package
// provider (apt, brew, winget, vcpkg, ...) implements, plus the common
// Logger and Params types they all take. Anything that needs to treat
// providers interchangeably — the toolchain layer, the environment
// layer, a future dispatcher — depends on this package instead of
// reaching into a specific provider.
package provider

// Params is the normalised install options passed down to a provider,
// independent of which package manager ends up handling the install.
type Params struct {
	// Version is a version constraint/prefix understood by the provider.
	// Empty means "latest".
	Version string
	// Platform is a target override, e.g. "debian:12", "ubuntu:22.04",
	// "macos", "macos:arm64", "windows:11". Empty means "this host".
	Platform string
	// DownloadOnly fetches artifacts without installing/linking them.
	DownloadOnly bool
}

// Logger receives structured events from a provider during Install/Remove.
type Logger interface {
	// DepsResolved fires once the full transitive dependency graph for
	// the requested package has been computed.
	DepsResolved(pkg string, preDeps, deps int)
	// Downloading fires when an artifact download starts. sizeBytes is
	// the server-reported size, or -1 if unknown.
	Downloading(name, version string, sizeBytes int64)
	// DownloadProgress fires repeatedly as bytes arrive.
	DownloadProgress(name string, received, total int64)
	// DownloadDone fires when a download completes.
	DownloadDone(name, version string)
	// Installing fires just before an artifact is unpacked/linked.
	Installing(name, version string, isPre, isDep bool)
	// Installed fires after an artifact is unpacked/linked successfully.
	Installed(name, version string, isPre, isDep bool)
	// Warn fires for non-fatal advisories.
	Warn(msg string)
}

// Provider is the interface every native package provider implements.
type Provider interface {
	Install(pkg string, params Params) error
	Remove(pkg string) error
	Resolve(pkg string) (string, error)
}

// Noop discards every event — the default when no logger is supplied.
var Noop Logger = noopLogger{}

type noopLogger struct{}

func (noopLogger) DepsResolved(_ string, _, _ int)       {}
func (noopLogger) Downloading(_, _ string, _ int64)      {}
func (noopLogger) DownloadProgress(_ string, _, _ int64) {}
func (noopLogger) DownloadDone(_, _ string)              {}
func (noopLogger) Installing(_, _ string, _, _ bool)     {}
func (noopLogger) Installed(_, _ string, _, _ bool)      {}
func (noopLogger) Warn(_ string)                         {}