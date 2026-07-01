package pkg

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/vertex-language/pkg/lib"
	"github.com/vertex-language/pkg/provider"
)

// LibResult is one native library ensured by Graph.EnsureNativeLibs: the
// Module that declared it, and the shared cache directory it was
// installed into (or already found in).
type LibResult struct {
	Module *Module
	Dir    string
}

// EnsureNativeLibs resolves every Module's vs.lib (if it has one) for
// the given host profile, and makes sure each resolved provider/target
// is installed via c.LibInstall. The result is in the same order as
// g.Modules, skipping modules with no vs.lib.
//
// Two modules that resolve to the identical provider kind, target
// fields, and library version share one cache entry: LibInstall keys
// purely off the resolved artifact, never off which module asked for
// it, so there's exactly one install on disk no matter how many modules
// depend on it.
func (g *Graph) EnsureNativeLibs(c *Cache, arch, osTag, hostRelease string, logger provider.Logger) ([]LibResult, error) {
	var results []LibResult
	for _, m := range g.Modules {
		if m.LibFile == nil {
			continue
		}
		pv, t, err := m.LibFile.Resolve(arch, osTag, hostRelease)
		if err != nil {
			return nil, fmt.Errorf("pkg: %s: resolving native library for %s-%s: %w", m.Path, arch, osTag, err)
		}
		dir, err := c.LibInstall(m.LibFile, pv, t, logger)
		if err != nil {
			return nil, fmt.Errorf("pkg: %s: installing native library: %w", m.Path, err)
		}
		results = append(results, LibResult{Module: m, Dir: dir})
	}
	return results, nil
}

// LibInstall ensures the artifact a resolved (Provider, Target) pair
// describes is installed, and returns its shared, content-addressed
// install directory: cache/lib/<hash>/{bin,lib,include}/... — the same
// layout provider.Provider.Install already writes into an envDir, just
// relocated under the shared cache instead of a per-project directory.
//
// The cache key hashes every field that determines what actually gets
// installed (kind, resolved package/URL/format/hash/lib/triplet, and
// the vs.lib file's own Version) — never the module path or version
// that happened to reference it.
func (c *Cache) LibInstall(lf *lib.File, pv *lib.Provider, t *lib.Target, logger provider.Logger) (string, error) {
	key := libInstallKey(lf, pv, t)

	unlock, err := c.lockLib(key)
	if err != nil {
		return "", err
	}
	defer unlock()

	dir := filepath.Join(c.dir, "lib", key)
	if _, err := os.Stat(dir); err == nil {
		return dir, nil // already installed by this or an earlier process
	}

	tmp := dir + ".tmp"
	os.RemoveAll(tmp) // clean up a half-finished install from a crashed run
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return "", fmt.Errorf("pkg: create %s: %w", tmp, err)
	}

	if err := installTarget(tmp, lf, pv, t, logger); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return "", fmt.Errorf("pkg: finalize install into %s: %w", dir, err)
	}
	return dir, nil
}

// libInstallKey hashes exactly the fields that determine an artifact's
// identity. Target's provider-level defaults (Rule 1) are already
// baked into t by the time lib.Parse returns it, so t's own fields are
// enough — except Hash, which Rule 2 never defaults onto the target,
// so a shared artifact's effective hash is read from pv instead.
func libInstallKey(lf *lib.File, pv *lib.Provider, t *lib.Target) string {
	hash := t.Hash
	if hash == "" {
		hash = pv.Hash
	}
	h := sha256.New()
	fmt.Fprintf(h, "kind=%s\npackage=%s\nurl=%s\nformat=%s\nhash=%s\nlib=%s\ntriplet=%s\nlibversion=%s\n",
		pv.Kind, t.Package, t.URL, t.Format, hash, t.Lib, t.VcpkgTriplet, lf.Version.Value)
	return hex.EncodeToString(h.Sum(nil))
}

// installTarget drives the correct install path for pv.Kind, writing
// into installDir (a scratch dir LibInstall renames into place on
// success).
func installTarget(installDir string, lf *lib.File, pv *lib.Provider, t *lib.Target, logger provider.Logger) error {
	switch pv.Kind {
	case lib.KindFetch:
		hash := t.Hash
		if hash == "" {
			hash = pv.Hash
		}
		return installFetch(installDir, t, hash)
	case lib.KindApt, lib.KindBrew, lib.KindVcpkg:
		return installManaged(installDir, lf, pv, t, logger)
	default:
		return fmt.Errorf("pkg: provider kind %q has no installer yet (dnf and pacman are recognized by pkg/lib but not yet wired to an installer here)", pv.Kind)
	}
}

// installManaged drives one of pkg/provider's package-manager-backed
// implementations (apt, brew, vcpkg — winget has no corresponding
// lib.Kind constant yet).
//
// NOT YET IMPLEMENTED: this needs a per-kind mapping from a vs.lib
// Target's (OS, Release) — e.g. OS "linux", Release "ubuntu-22.04" — to
// that provider's own Platform string syntax (apt: "ubuntu:22.04";
// brew: "linux"/"linux:arm64"; vcpkg has no Platform concept at all —
// it takes arch + t.VcpkgTriplet directly via ResolveTriplet). That
// mapping is provider-specific enough to deserve its own design pass
// rather than a guessed string transform here.
func installManaged(installDir string, lf *lib.File, pv *lib.Provider, t *lib.Target, logger provider.Logger) error {
	return fmt.Errorf("pkg: provider kind %q: managed-provider install is not yet implemented (see installManaged's doc comment)", pv.Kind)
}

// installFetch handles lib.KindFetch directly: a raw URL download,
// verified against effectiveHash, extracted by t.Format. Unlike
// apt/brew/vcpkg, "fetch" has no package-manager backend at all —
// pkg/provider has no implementation for it because there's nothing
// OS-specific to normalize, so it's handled here instead.
func installFetch(installDir string, t *lib.Target, effectiveHash string) error {
	resp, err := http.Get(t.URL)
	if err != nil {
		return fmt.Errorf("pkg: fetch %s: %w", t.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pkg: fetch %s: status %d", t.URL, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("pkg: fetch %s: %w", t.URL, err)
	}

	if effectiveHash != "" {
		sum := sha256.Sum256(data)
		got := "h1:" + hex.EncodeToString(sum[:])
		if got != effectiveHash {
			return fmt.Errorf("pkg: fetch %s: content hash %s does not match vs.lib's declared hash %s", t.URL, got, effectiveHash)
		}
	}

	return extractArchive(data, t.Format, installDir)
}

// extractArchive supports the two most common fetch formats directly.
//
// Known gap: bz2/xz/zst are not wired up here, even though
// pkg/provider/apt already supports all three for .deb unpacking —
// worth factoring into one shared archive helper both packages use,
// rather than duplicating decompressor selection a second time.
func extractArchive(data []byte, format, destDir string) error {
	switch format {
	case "zip", "":
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return fmt.Errorf("pkg: fetch: open zip: %w", err)
		}
		for _, f := range zr.File {
			if err := extractZipEntry(f, destDir); err != nil {
				return err
			}
		}
		return nil

	case "tar.gz", "tgz":
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("pkg: fetch: gunzip: %w", err)
		}
		defer gz.Close()
		return extractTar(tar.NewReader(gz), destDir)

	default:
		return fmt.Errorf("pkg: fetch: unsupported format %q (zip and tar.gz are supported)", format)
	}
}

func extractZipEntry(f *zip.File, destDir string) error {
	path := filepath.Join(destDir, filepath.FromSlash(f.Name))
	if f.FileInfo().IsDir() {
		return os.MkdirAll(path, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("pkg: fetch: open %s in zip: %w", f.Name, err)
	}
	defer rc.Close()
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
	if err != nil {
		return fmt.Errorf("pkg: fetch: create %s: %w", path, err)
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}

func extractTar(tr *tar.Reader, destDir string) error {
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("pkg: fetch: read tar: %w", err)
		}
		path := filepath.Join(destDir, filepath.FromSlash(h.Name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode))
			if err != nil {
				return fmt.Errorf("pkg: fetch: create %s: %w", path, err)
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		}
	}
}