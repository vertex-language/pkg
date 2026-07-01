package pkg

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/vertex-language/pkg/parser/mod"
)

// Mod returns the extracted, read-only source directory for path@version
// — from cache if already present, otherwise fetched via c.fetcher,
// extracted, and verified/recorded against sumPath (a project's vs.sum).
//
// version must already be a canonical version or pseudo-version — what
// importer.Fetcher.Resolve returns. Mod never itself decides "latest";
// Load (or `vertex mod get`) does that once, up front, and records the
// exact result in vs.mod.
//
// mode controls what happens when path@version has no vs.sum entry yet:
// ModReadonly fails closed — the situation a plain `vertex build`
// against a committed vs.mod should never hit; ModUpdate computes and
// records one. An entry that already exists is always verified
// regardless of mode — mode only gates adding new trust, never skips
// checking it.
func (c *Cache) Mod(path mod.ModulePath, version string, sumPath string, mode LoadMode) (dir string, err error) {
	mv := mod.ModuleVersion{Path: path, Version: version}

	unlock, err := c.lockModule(mv)
	if err != nil {
		return "", err
	}
	defer unlock()

	dir = c.modDir(path, version)

	sums, err := readSum(sumPath)
	if err != nil {
		return "", err
	}
	wantHash, known := sums[mv]

	if _, statErr := os.Stat(dir); statErr == nil {
		// Already extracted — possibly by another project sharing this
		// cache. Re-verify against *this* project's vs.sum every time
		// rather than trusting the directory's mere existence: vs.sum
		// is what a clone of this project ships and checks, not the
		// cache.
		gotHash, err := hashDir(dir)
		if err != nil {
			return "", err
		}
		if known {
			if gotHash != wantHash {
				return "", fmt.Errorf("pkg: %s@%s: cache entry hash %s does not match vs.sum entry %s (corrupted cache, or vs.sum was edited)", path, version, gotHash, wantHash)
			}
			return dir, nil
		}
		if mode == ModReadonly {
			return "", fmt.Errorf("pkg: %s@%s: present in cache but missing from %s; run `vertex mod get %s@%s` or pass -mod=mod", path, version, sumPath, path, version)
		}
		sums[mv] = gotHash
		if err := writeSum(sumPath, sums); err != nil {
			return "", err
		}
		return dir, nil
	}

	if !known && mode == ModReadonly {
		return "", fmt.Errorf("pkg: %s@%s: not found in %s and -mod=readonly forbids fetching an unrecorded version; run `vertex mod get %s@%s` or pass -mod=mod", path, version, sumPath, path, version)
	}

	tmp, err := os.MkdirTemp(filepath.Join(c.dir, "mod"), ".tmp-*")
	if err != nil {
		return "", fmt.Errorf("pkg: create scratch dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	if err := c.fetcher.Fetch(path, version, tmp); err != nil {
		return "", fmt.Errorf("pkg: fetch %s@%s: %w", path, version, err)
	}

	gotHash, err := hashDir(tmp)
	if err != nil {
		return "", err
	}
	if known && gotHash != wantHash {
		return "", fmt.Errorf("pkg: %s@%s: fetched content hash %s does not match vs.sum entry %s — upstream may have changed, or vs.sum is wrong", path, version, gotHash, wantHash)
	}

	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", fmt.Errorf("pkg: mkdir: %w", err)
	}
	if err := os.Rename(tmp, dir); err != nil {
		return "", fmt.Errorf("pkg: install %s@%s into cache: %w", path, version, err)
	}
	if err := makeReadonly(dir); err != nil {
		return "", err
	}

	if !known {
		sums[mv] = gotHash
		if err := writeSum(sumPath, sums); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// modDir returns cache/mod/<module path>@<version>/. The module path's
// own "/" characters become real nested directories — the same layout
// Go's own module cache uses under $GOPATH/pkg/mod.
//
// Known gap: unlike Go's module cache, module paths aren't
// case-escaped, so two paths differing only in case (rare, but legal)
// would collide on a case-insensitive filesystem. Flagging rather than
// silently mishandling, same spirit as mod.IsValidModulePath's
// documented gaps.
func (c *Cache) modDir(path mod.ModulePath, version string) string {
	return filepath.Join(c.dir, "mod", string(path)+"@"+version)
}

// makeReadonly recursively strips write permission from dir, matching
// Go's own module cache: extracted source is never meant to be edited
// in place, and read-only permissions catch an accidental write (e.g.
// a build script patching a vendored file) loudly and immediately
// instead of silently corrupting a cache entry shared machine-wide.
func makeReadonly(dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.Chmod(p, info.Mode()&^0o222)
	})
}