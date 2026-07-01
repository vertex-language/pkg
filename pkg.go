// Package pkg is the top-level composition layer for the Vertex
// toolchain: it owns the on-disk module and native-library cache rooted
// at $VERTEX_HOME, resolves a project's vs.mod into a full dependency
// Graph (via pkg/mod and pkg/importer), and ensures every native
// library a graph's vs.lib files need is installed (via pkg/lib,
// pkg/provider, and pkg/toolchain).
//
// Nothing below this package — mod, lib, importer, provider, toolchain
// — knows about $VERTEX_HOME, vs.sum, or any particular project. This
// is the only package that does; everything else takes a filename, a
// dir, or a reader and returns a value, with no notion of a shared
// cache or of "the current project."
package pkg

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/vertex-language/pkg/importer"
)

// Home resolves $VERTEX_HOME's effective value given an explicit
// override (typically a -vertex-home CLI flag; pass "" if none was
// given). Precedence: override > $VERTEX_HOME env var > ~/.vertex.
//
// This is the only place in pkg that reads the environment. Callers
// resolve Home once and pass the result into OpenCache explicitly, so
// the rest of this package — and everything that tests against it —
// stays blind to ambient state.
func Home(override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	if v := os.Getenv("VERTEX_HOME"); v != "" {
		return filepath.Abs(v)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("pkg: cannot determine home directory (set VERTEX_HOME or pass -vertex-home explicitly): %w", err)
	}
	return filepath.Join(home, ".vertex"), nil
}

// LoadMode governs whether Cache.Mod and Load are allowed to fetch a
// version with no existing vs.sum entry, or only verify against entries
// that already exist.
type LoadMode int

const (
	// ModReadonly never fetches an unrecorded version and never writes
	// vs.sum. The default — matching modern Go's own default — since a
	// shared, machine-wide cache is exactly the place where a plain
	// `vertex build` silently mutating vs.sum or pulling a new version
	// is the wrong default behavior.
	ModReadonly LoadMode = iota
	// ModUpdate allows resolving and recording new vs.sum entries, for
	// `vertex mod get`/`tidy`.
	ModUpdate
)

// Cache is the on-disk module and native-library cache rooted at
// homeDir/cache. One Cache is meant to be shared, read and written, by
// every vertex invocation on the machine — this is what lets two
// unrelated projects share a fetched module or an installed native
// library instead of duplicating it per project.
//
// Cache is safe for concurrent use across processes: every mutating
// operation (Mod, LibInstall) takes a per-key file lock (lock.go)
// around its check-then-fetch-then-extract sequence.
type Cache struct {
	dir     string
	fetcher importer.Fetcher
}

// Dir returns the cache's root directory (homeDir/cache).
func (c *Cache) Dir() string { return c.dir }

// OpenCache opens (creating if necessary) the cache rooted at
// homeDir/cache. fetcher retrieves modules that aren't already cached —
// pass importer.NewGitFetcher() for the normal case.
func OpenCache(homeDir string, fetcher importer.Fetcher) (*Cache, error) {
	dir := filepath.Join(homeDir, "cache")
	for _, sub := range []string{"download", "mod", "lock", "lib"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, fmt.Errorf("pkg: init cache dir %s: %w", sub, err)
		}
	}
	return &Cache{dir: dir, fetcher: fetcher}, nil
}