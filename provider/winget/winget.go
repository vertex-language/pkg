package winget

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/vertex-language/pkg/provider"
)

// Params is an alias for provider.Params — see pkg/provider/provider.go.
type Params = provider.Params

// Winget is the provider for Windows Package Manager packages.
// It is pure HTTP + file I/O — no winget.exe, no Windows APIs, runs on any host.
// Packages are resolved and downloaded from the public winget-pkgs GitHub repo.
type Winget struct {
	envDir   string
	binDir   string
	cacheDir string
	arch     string // winget architecture string: x64 | arm64 | x86
	logger   Logger
}

var _ provider.Provider = (*Winget)(nil)

// New returns a Winget provider rooted at envDir.
// Pass a nil logger to silence all output.
func New(envDir string, logger Logger) (*Winget, error) {
	if logger == nil {
		logger = provider.Noop
	}
	cacheDir := filepath.Join(os.TempDir(), "env-winget-cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("winget: init cache dir: %w", err)
	}
	return &Winget{
		envDir:   envDir,
		binDir:   filepath.Join(envDir, "bin"),
		cacheDir: cacheDir,
		arch:     goArchToWinget(runtime.GOARCH),
		logger:   logger,
	}, nil
}

// Install resolves, downloads, and unpacks a winget package into the environment.
//
// pkg must be a winget PackageIdentifier: "Publisher.Package"
// e.g. "Microsoft.PowerShell", "Google.Chrome", "Neovim.Neovim".
func (w *Winget) Install(pkg string, params Params) error {
	manifest, err := fetchInstallerManifest(pkg, params.Version)
	if err != nil {
		return fmt.Errorf("install %s: %w", pkg, err)
	}

	installer, err := selectInstaller(manifest, w.arch)
	if err != nil {
		return fmt.Errorf("install %s: %w", pkg, err)
	}

	// winget has no transitive dependency graph — always 0/0.
	w.logger.DepsResolved(pkg, 0, 0)

	localPath, err := w.download(pkg, manifest.PackageVersion, installer)
	if err != nil {
		return fmt.Errorf("install %s: %w", pkg, err)
	}

	if params.DownloadOnly {
		return nil
	}

	w.logger.Installing(pkg, manifest.PackageVersion, false, false)

	if err := unpack(localPath, installer, w.envDir); err != nil {
		return fmt.Errorf("install %s: unpack: %w", pkg, err)
	}

	w.logger.Installed(pkg, manifest.PackageVersion, false, false)

	return nil
}

// Remove deletes the package's files from the environment's bin directory.
// We do a best-effort match by the short package name since we don't track
// which specific files were placed (a future index.toml extension could do so).
func (w *Winget) Remove(pkg string) error {
	short := shortName(pkg)
	entries, err := os.ReadDir(w.binDir)
	if err != nil {
		return nil // bin may not exist yet
	}
	for _, e := range entries {
		base := strings.TrimSuffix(e.Name(), ".exe")
		if strings.EqualFold(base, short) {
			os.Remove(filepath.Join(w.binDir, e.Name()))
		}
	}
	return nil
}

// Resolve normalises the package name. For winget the identifier is already
// canonical, so this is a pass-through.
func (w *Winget) Resolve(pkg string) (string, error) {
	return pkg, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// goArchToWinget maps Go's GOARCH values to winget Architecture strings.
func goArchToWinget(goarch string) string {
	switch goarch {
	case "amd64":
		return "x64"
	case "arm64":
		return "arm64"
	case "386":
		return "x86"
	default:
		return "x64"
	}
}

// shortName returns the last dot-delimited segment of a PackageIdentifier.
// "Microsoft.PowerShell" → "PowerShell"
// "Neovim.Neovim"       → "Neovim"
func shortName(id string) string {
	if i := strings.LastIndexByte(id, '.'); i >= 0 {
		return id[i+1:]
	}
	return id
}