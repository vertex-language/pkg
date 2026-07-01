// Package toolchain makes sure the native build tools a from-source
// provider needs (cmake, ninja, a compiler, ...) are present on the host,
// installing any that are missing via whichever native provider owns the
// current OS. It exists so that from-source providers like vcpkg never
// need to import apt/brew/winget themselves.
package toolchain

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/vertex-language/pkg/provider"
	"github.com/vertex-language/pkg/provider/apt"
	"github.com/vertex-language/pkg/provider/brew"
	"github.com/vertex-language/pkg/provider/winget"
)

// Requirement is one build tool a from-source provider needs on PATH,
// plus the native package that satisfies it per host OS.
type Requirement struct {
	Bin    string            // binary probed on PATH, e.g. "cmake"
	PkgFor map[string]string // GOOS -> package name; an absent OS is unsupported
}

// BuildTools is the requirement set for compiling C/C++ ports from source.
// Exported so other from-source providers besides vcpkg can reuse it.
var BuildTools = []Requirement{
	{Bin: "cmake", PkgFor: map[string]string{
		"linux": "cmake", "darwin": "cmake", "windows": "Kitware.CMake",
	}},
	{Bin: "ninja", PkgFor: map[string]string{
		"linux": "ninja-build", "darwin": "ninja", "windows": "Ninja-build.Ninja",
	}},
	{Bin: "gcc", PkgFor: map[string]string{
		"linux": "gcc", "darwin": "gcc",
		// no windows entry — MSVC is expected via the VS Build Tools
		// installer; there's no winget package installed on its behalf.
	}},
	{Bin: "g++", PkgFor: map[string]string{
		"linux": "g++",
		// darwin's "gcc" formula already provides g++.
	}},
}

// EnsureBuildTools is a convenience wrapper over Ensure for the common
// case: make sure cmake/ninja/a C and C++ compiler are available. This is
// what the vcpkg provider calls before compiling a port.
func EnsureBuildTools(envPath string, logger provider.Logger) error {
	return Ensure(envPath, BuildTools, logger)
}

// Ensure checks that every Requirement.Bin in reqs is on PATH, installing
// any that are missing via the native provider for the host OS.
func Ensure(envPath string, reqs []Requirement, logger provider.Logger) error {
	if logger == nil {
		logger = provider.Noop
	}

	var missing []string
	for _, r := range reqs {
		pkg, ok := r.PkgFor[runtime.GOOS]
		if !ok {
			continue // this requirement doesn't apply to the host OS
		}
		if _, err := exec.LookPath(r.Bin); err != nil {
			missing = append(missing, pkg)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	logger.Warn(fmt.Sprintf("toolchain: installing missing build tools: %v", missing))
	return installNative(envPath, missing, logger)
}

// installNative installs pkgs using whichever native provider owns the host OS.
func installNative(envPath string, pkgs []string, logger provider.Logger) error {
	switch runtime.GOOS {
	case "linux":
		p, err := apt.New(envPath, logger)
		if err != nil {
			return fmt.Errorf("toolchain: init apt: %w", err)
		}
		for _, pkg := range pkgs {
			if err := p.Install(pkg, apt.Params{}); err != nil {
				return fmt.Errorf("toolchain: apt install %s: %w", pkg, err)
			}
		}

	case "darwin":
		p, err := brew.New(envPath, logger)
		if err != nil {
			return fmt.Errorf("toolchain: init brew: %w", err)
		}
		for _, pkg := range pkgs {
			if err := p.Install(pkg, brew.Params{}); err != nil {
				return fmt.Errorf("toolchain: brew install %s: %w", pkg, err)
			}
		}

	case "windows":
		p, err := winget.New(envPath, logger)
		if err != nil {
			return fmt.Errorf("toolchain: init winget: %w", err)
		}
		for _, pkg := range pkgs {
			if err := p.Install(pkg, winget.Params{}); err != nil {
				return fmt.Errorf("toolchain: winget install %s: %w", pkg, err)
			}
		}

	default:
		return fmt.Errorf("toolchain: unsupported OS %q — install %v manually", runtime.GOOS, pkgs)
	}
	return nil
}