package vcpkg

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/vertex-language/pkg/provider"
	"github.com/vertex-language/pkg/toolchain"
)

// Logger is an alias for provider.Logger — see pkg/provider/provider.go.
type Logger = provider.Logger

// Params is an alias for provider.Params — see pkg/provider/provider.go.
type Params = provider.Params

// Vcpkg is the vcpkg provider. One instance is created per environment.
// It owns the vcpkg clone and installed-package tree that live inside the
// env, and — unlike apt/brew/winget — compiles ports from source rather
// than unpacking prebuilt artifacts. Getting a working build toolchain
// onto the host lives in pkg/toolchain, not here: this package only
// drives vcpkg itself once cmake/ninja/a compiler are already on PATH.
type Vcpkg struct {
	envPath    string
	vcpkgDir   string
	installDir string
	logger     Logger
}

var _ provider.Provider = (*Vcpkg)(nil)

// New returns a Vcpkg provider rooted at envPath. The vcpkg binary is not
// required to exist yet; ensureBootstrapped() is called lazily on first use.
func New(envPath string, logger Logger) (*Vcpkg, error) {
	if logger == nil {
		logger = provider.Noop
	}
	return &Vcpkg{
		envPath:    envPath,
		vcpkgDir:   filepath.Join(envPath, "vcpkg"),
		installDir: filepath.Join(envPath, "vcpkg_installed"),
		logger:     logger,
	}, nil
}

// Install builds and installs pkg into the environment.
func (v *Vcpkg) Install(pkg string, params Params) error {
	if err := v.ensureBootstrapped(); err != nil {
		return err
	}
	if err := toolchain.EnsureBuildTools(v.envPath, v.logger); err != nil {
		return err
	}

	t, err := ResolveTriplet(params.Platform)
	if err != nil {
		return fmt.Errorf("vcpkg install %s: %w", pkg, err)
	}

	v.logger.Installing(pkg, params.Version, false, false)
	if err := v.runInstall(pkg, params.Version, t, params.DownloadOnly); err != nil {
		return err
	}
	v.logger.Installed(pkg, params.Version, false, false)

	return v.linkBins(t)
}

// Remove uninstalls pkg from the environment.
func (v *Vcpkg) Remove(pkg string) error {
	if err := v.ensureBootstrapped(); err != nil {
		return err
	}
	t, err := ResolveTriplet("")
	if err != nil {
		return fmt.Errorf("vcpkg remove %s: %w", pkg, err)
	}
	return v.runRemove(pkg, t)
}

// Resolve normalises a package name for vcpkg (lowercase, hyphens only).
func (v *Vcpkg) Resolve(pkg string) (string, error) {
	return normaliseName(pkg), nil
}

func (v *Vcpkg) binaryPath() string {
	name := "vcpkg"
	if runtime.GOOS == "windows" {
		name = "vcpkg.exe"
	}
	return filepath.Join(v.vcpkgDir, name)
}

func (v *Vcpkg) downloadsDir() string {
	return filepath.Join(v.envPath, "vcpkg_downloads")
}

func (v *Vcpkg) ensureDirs() error {
	for _, d := range []string{v.installDir, v.downloadsDir()} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("vcpkg: mkdir %s: %w", d, err)
		}
	}
	return nil
}