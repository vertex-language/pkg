package vcpkg

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// runInstall executes vcpkg install for the given package, version, and triplet.
//
// Version pinning strategy:
//   - No version → classic mode:  vcpkg install pkg:triplet
//   - With version → manifest mode: write a temp vcpkg.json + vcpkg-configuration.json,
//     then vcpkg install --x-manifest-root=<tmpdir>
//
// In both cases packages are laid out under v.installDir.
func (v *Vcpkg) runInstall(pkg, version, triplet string, downloadOnly bool) error {
	if version == "" {
		return v.classicInstall(pkg, triplet, downloadOnly)
	}
	return v.manifestInstall(pkg, version, triplet, downloadOnly)
}

// classicInstall runs vcpkg install in classic mode (no version constraint).
func (v *Vcpkg) classicInstall(pkg, triplet string, downloadOnly bool) error {
	pkgRef := normaliseName(pkg) + ":" + triplet
	args := v.baseArgs("install", pkgRef)
	if downloadOnly {
		args = append(args, "--only-downloads")
	}
	return v.exec(args)
}

// manifestInstall writes a minimal vcpkg.json with an exact version override
// and runs vcpkg install in manifest mode.
func (v *Vcpkg) manifestInstall(pkg, version, triplet string, downloadOnly bool) error {
	tmpDir, err := os.MkdirTemp("", "env-vcpkg-*")
	if err != nil {
		return fmt.Errorf("vcpkg: create manifest temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := writeManifest(tmpDir, pkg, version); err != nil {
		return err
	}

	// vcpkg-configuration.json — point at the default curated registry.
	if err := writeConfiguration(tmpDir, v.vcpkgDir); err != nil {
		return err
	}

	args := v.baseArgs("install",
		"--x-manifest-root="+tmpDir,
		"--triplet="+triplet,
	)
	if downloadOnly {
		args = append(args, "--only-downloads")
	}
	return v.exec(args)
}

// runRemove executes vcpkg remove for the given package and triplet.
func (v *Vcpkg) runRemove(pkg, triplet string) error {
	pkgRef := normaliseName(pkg) + ":" + triplet
	return v.exec(v.baseArgs("remove", "--recurse", pkgRef))
}

// baseArgs builds the common vcpkg argument list shared by install and remove.
func (v *Vcpkg) baseArgs(subcmd string, extra ...string) []string {
	args := []string{
		subcmd,
		"--x-install-root=" + v.installDir,
		"--downloads-root=" + v.downloadsDir(),
		"--disable-metrics",
		"--no-print-usage",
	}
	return append(args, extra...)
}

// exec runs the vcpkg binary with the supplied arguments, inheriting stdout/
// stderr so the caller sees build output in real time.
func (v *Vcpkg) exec(args []string) error {
	cmd := exec.Command(v.binaryPath(), args...)
	cmd.Env = append(os.Environ(), "VCPKG_ROOT="+v.vcpkgDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("vcpkg: %s: %w", args[0], err)
	}
	return nil
}

// linkBins hard-links (or copies on Windows) binaries produced by vcpkg into
// the environment's bin/ directory so they appear on PATH when the env is
// active.
func (v *Vcpkg) linkBins(triplet string) error {
	srcBin := filepath.Join(v.installDir, triplet, "tools")
	dstBin := filepath.Join(v.envPath, "bin")

	entries, err := os.ReadDir(srcBin)
	if os.IsNotExist(err) {
		return nil // no tools directory — library-only package, nothing to link
	}
	if err != nil {
		return fmt.Errorf("vcpkg: read tools dir: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() {
			// vcpkg nests tool binaries one level under a per-package subdir.
			if err := linkDirBins(filepath.Join(srcBin, e.Name()), dstBin); err != nil {
				return err
			}
			continue
		}
		if isExecutable(e) {
			if err := linkFile(filepath.Join(srcBin, e.Name()), filepath.Join(dstBin, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func linkDirBins(src, dstBin string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("vcpkg: read tools subdir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() && isExecutable(e) {
			if err := linkFile(filepath.Join(src, e.Name()), filepath.Join(dstBin, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// linkFile hard-links src → dst, replacing any existing file at dst.
func linkFile(src, dst string) error {
	_ = os.Remove(dst) // ignore error — file may not exist yet
	if err := os.Link(src, dst); err != nil {
		// Hard-link may fail across devices; fall back to a symlink.
		if err2 := os.Symlink(src, dst); err2 != nil {
			return fmt.Errorf("vcpkg: link %s → %s: %w", src, dst, err)
		}
	}
	return nil
}

func isExecutable(e os.DirEntry) bool {
	info, err := e.Info()
	if err != nil {
		return false
	}
	return info.Mode()&0111 != 0
}

// ── manifest helpers ──────────────────────────────────────────────────────────

// vcpkgManifest is the minimal vcpkg.json structure needed for version pinning.
type vcpkgManifest struct {
	Name         string              `json:"name"`
	Version      string              `json:"version"`
	Dependencies []vcpkgDependency   `json:"dependencies"`
	Overrides    []vcpkgVersionEntry `json:"overrides"`
}

type vcpkgDependency struct {
	Name string `json:"name"`
}

type vcpkgVersionEntry struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// vcpkgConfiguration is the minimal vcpkg-configuration.json structure.
type vcpkgConfiguration struct {
	DefaultRegistry vcpkgRegistry `json:"default-registry"`
}

type vcpkgRegistry struct {
	Kind       string `json:"kind"`
	Baseline   string `json:"baseline"`
	Repository string `json:"repository"`
}

func writeManifest(dir, pkg, version string) error {
	name := normaliseName(pkg)
	m := vcpkgManifest{
		Name:    "env-managed",
		Version: "0.0.0",
		Dependencies: []vcpkgDependency{
			{Name: name},
		},
		Overrides: []vcpkgVersionEntry{
			{Name: name, Version: version},
		},
	}
	return writeJSON(filepath.Join(dir, "vcpkg.json"), m)
}

func writeConfiguration(dir, vcpkgDir string) error {
	// Resolve the HEAD commit of the local clone to use as the baseline.
	out, err := exec.Command("git", "-C", vcpkgDir, "rev-parse", "HEAD").Output()
	if err != nil {
		return fmt.Errorf("vcpkg: resolve baseline commit: %w", err)
	}
	baseline := strings.TrimSpace(string(out))

	cfg := vcpkgConfiguration{
		DefaultRegistry: vcpkgRegistry{
			Kind:       "git",
			Baseline:   baseline,
			Repository: "https://github.com/microsoft/vcpkg",
		},
	}
	return writeJSON(filepath.Join(dir, "vcpkg-configuration.json"), cfg)
}

func writeJSON(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("vcpkg: create %s: %w", path, err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("vcpkg: write %s: %w", path, err)
	}
	return nil
}