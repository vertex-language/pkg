package apt

import (
	"fmt"
	"os"
	"path/filepath"
)

// Params is what the top-level passes down to the apt provider.
type Params struct {
	Version      string
	Platform     string
	DownloadOnly bool
}

// Apt is the provider for Debian/Ubuntu package installs.
// It is pure file I/O — no apt-get, no system calls, runs on any host OS.
type Apt struct {
	envDir   string
	binDir   string
	cacheDir string
	logger   Logger // nil = silent
}

// New returns an Apt provider rooted at envDir.
// Pass a nil logger to silence all output.
func New(envDir string, logger Logger) (*Apt, error) {
	cacheDir := filepath.Join(os.TempDir(), "env-apt-cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("apt: init cache dir: %w", err)
	}
	return &Apt{
		envDir:   envDir,
		binDir:   filepath.Join(envDir, "bin"),
		cacheDir: cacheDir,
		logger:   logger,
	}, nil
}

// Install fetches and unpacks a package and all its transitive dependencies.
//
// Installation order matches dpkg policy:
//  1. Pre-Depends (must be configured before the target package is unpacked)
//  2. Regular Depends (transitive closure, BFS order)
//  3. The requested package itself
func (a *Apt) Install(pkg string, params Params) error {
	target := params.Platform
	if target == "" {
		target = "debian:12"
	}

	img, err := resolveImage(target)
	if err != nil {
		return fmt.Errorf("apt install: %w", err)
	}

	index, err := fetchPackageIndex(img)
	if err != nil {
		return fmt.Errorf("apt install: fetch index: %w", err)
	}

	meta, err := findPackage(index, pkg, params.Version)
	if err != nil {
		return fmt.Errorf("apt install: %w", err)
	}

	plan, err := resolveDeps(meta, index, a.logger)
	if err != nil {
		return fmt.Errorf("apt install: resolve deps: %w", err)
	}

	if a.logger != nil {
		a.logger.DepsResolved(pkg, len(plan.PreDeps), len(plan.Deps))
	}

	if params.DownloadOnly {
		allPkgs := append([]packageMeta{meta}, plan.PreDeps...)
		allPkgs = append(allPkgs, plan.Deps...)
		for _, m := range allPkgs {
			if _, err := a.download(img, m); err != nil {
				return fmt.Errorf("apt install: download %s: %w", m.Name, err)
			}
		}
		return nil
	}

	for _, dep := range plan.PreDeps {
		if err := a.installOne(img, dep, true, true); err != nil {
			return fmt.Errorf("apt install: pre-dep %s: %w", dep.Name, err)
		}
	}

	for _, dep := range plan.Deps {
		if err := a.installOne(img, dep, false, true); err != nil {
			return fmt.Errorf("apt install: dep %s: %w", dep.Name, err)
		}
	}

	return a.installOne(img, meta, false, false)
}

// Remove deletes a package's binary from the environment bin dir.
func (a *Apt) Remove(pkg string) error {
	target := filepath.Join(a.binDir, pkg)
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("apt remove %s: %w", pkg, err)
	}
	return nil
}

// Resolve normalises a package name for apt (passthrough for most packages).
func (a *Apt) Resolve(pkg string) (string, error) { return pkg, nil }

// installOne downloads and unpacks a single package into the environment root.
// isPre and isDep are passed straight through to the logger for context.
func (a *Apt) installOne(img image, meta packageMeta, isPre, isDep bool) error {
	debPath, err := a.download(img, meta)
	if err != nil {
		return err
	}

	if a.logger != nil {
		a.logger.Installing(meta.Name, meta.Version, isPre, isDep)
	}

	if err := unpack(debPath, a.envDir); err != nil {
		return err
	}

	if a.logger != nil {
		a.logger.Installed(meta.Name, meta.Version, isPre, isDep)
	}

	return nil
}