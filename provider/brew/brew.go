package brew

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Params is what the top-level passes down to the brew provider.
type Params struct {
	Version      string
	Platform     string // "macos", "macos:arm64", "linux", "linux:arm64", etc.
	DownloadOnly bool   // fetch bottle but skip extraction and linking
}

// Brew is the provider for Homebrew formula installs.
// It is pure network + file I/O — no brew CLI required, runs on any host OS.
type Brew struct {
	envDir   string
	binDir   string
	cacheDir string
	client   *http.Client
	logger   Logger
}

// New returns a Brew provider rooted at envDir.
// Pass a nil logger to silence all output.
func New(envDir string, logger Logger) (*Brew, error) {
	cacheDir := filepath.Join(os.TempDir(), "env-brew-cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("brew: init cache dir: %w", err)
	}
	return &Brew{
		envDir:   envDir,
		binDir:   filepath.Join(envDir, "bin"),
		cacheDir: cacheDir,
		client:   &http.Client{},
		logger:   logger,
	}, nil
}

// Install fetches and unpacks a formula bottle and all its transitive deps.
//
// Installation order:
//  1. Transitive dependencies (BFS, deepest first due to queue ordering)
//  2. The requested formula itself
func (b *Brew) Install(pkg string, params Params) error {
	meta, err := fetchFormula(pkg)
	if err != nil {
		return fmt.Errorf("brew install %s: %w", pkg, err)
	}

	if params.Version != "" && !strings.HasPrefix(meta.Version, params.Version) {
		return fmt.Errorf("brew install %s: requested %q, available %q",
			pkg, params.Version, meta.Version)
	}

	tag, err := preferredBottleTag(runtime.GOOS, runtime.GOARCH, params.Platform)
	if err != nil {
		return fmt.Errorf("brew install %s: %w", pkg, err)
	}

	deps, err := resolveDeps(meta, b.logger)
	if err != nil {
		return fmt.Errorf("brew install %s: resolve deps: %w", pkg, err)
	}

	if b.logger != nil {
		b.logger.DepsResolved(pkg, 0, len(deps))
	}

	for _, dep := range deps {
		if err := b.installOne(dep, tag, params.DownloadOnly, true); err != nil {
			return fmt.Errorf("brew install %s: dep %s: %w", pkg, dep.Name, err)
		}
	}

	return b.installOne(meta, tag, params.DownloadOnly, false)
}

// Remove unlinks a formula from the environment and deletes its Cellar keg.
func (b *Brew) Remove(pkg string) error {
	// Remove all symlinks in linked dirs that resolve into this keg.
	kegPrefix := filepath.Join(b.envDir, "Cellar", pkg)
	for _, dir := range cellarLinkDirs {
		dstDir := filepath.Join(b.envDir, dir)
		entries, err := os.ReadDir(dstDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			path := filepath.Join(dstDir, e.Name())
			target, err := os.Readlink(path)
			if err != nil {
				continue
			}
			if strings.HasPrefix(target, kegPrefix) {
				os.Remove(path)
			}
		}
	}

	// Remove the Cellar keg itself.
	kegDir := filepath.Join(b.envDir, "Cellar", pkg)
	if err := os.RemoveAll(kegDir); err != nil {
		return fmt.Errorf("brew remove %s: remove keg: %w", pkg, err)
	}
	return nil
}

// Resolve normalises a package name for brew (passthrough for most formulae).
func (b *Brew) Resolve(pkg string) (string, error) { return pkg, nil }

// resolveBottle returns the bottleFile for the given tag, falling back through
// the macOS version ladder when the preferred tag is absent.
func (b *Brew) resolveBottle(meta formulaMeta, preferredTag string) (bottleFile, error) {
	if f, ok := meta.Bottles[preferredTag]; ok {
		return f, nil
	}
	// On macOS, try older release tags before giving up.
	for _, fallback := range macOSTagFallbackOrder(hostArch()) {
		if f, ok := meta.Bottles[fallback]; ok {
			if b.logger != nil {
				b.logger.Warn(fmt.Sprintf(
					"%s: bottle tag %q unavailable, using %q",
					meta.Name, preferredTag, fallback,
				))
			}
			return f, nil
		}
	}
	return findBottle(meta, preferredTag) // returns a descriptive error
}

// installOne downloads and (optionally) unpacks a single formula.
func (b *Brew) installOne(meta formulaMeta, tag string, downloadOnly, isDep bool) error {
	file, err := b.resolveBottle(meta, tag)
	if err != nil {
		return err
	}

	bottlePath, err := b.download(meta, file)
	if err != nil {
		return err
	}

	if downloadOnly {
		return nil
	}

	if b.logger != nil {
		b.logger.Installing(meta.Name, meta.Version, false, isDep)
	}

	if err := unpack(bottlePath, meta.Name, meta.kegVersion(), b.envDir); err != nil {
		return err
	}

	if b.logger != nil {
		b.logger.Installed(meta.Name, meta.Version, false, isDep)
	}

	return nil
}