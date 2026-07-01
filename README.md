# pkg

Package `pkg` is the top-level composition layer for the Vertex toolchain: it
owns the on-disk module and native-library cache rooted at `$VERTEX_HOME`,
resolves a project's `vs.mod` into a full dependency `Graph`, and ensures
every native library a graph's `vs.lib` files need is installed — all shared,
machine-wide, across every project that uses it.

```go
import "github.com/vertex-language/pkg"
```

## Overview

Nothing below this package — `mod`, `lib`, `importer`, `provider`,
`toolchain` — knows about `$VERTEX_HOME`, `vs.sum`, or any particular
project. Each of those takes a filename, a directory, or a reader and
returns a value, with no notion of a shared cache. `pkg` is the only package
that owns that state:

- **`mod`** parses/formats a single `vs.mod` file.
- **`lib`** parses/formats a single `vs.lib` file and resolves it against a
  host profile.
- **`importer`** fetches one `(module path, version)` from its git remote
  into a directory the caller supplies — no cache, no layout, no lockfiles.
- **`provider`** (and `apt`/`brew`/`vcpkg`/`winget`) installs one package
  into one `envDir`.
- **`toolchain`** makes sure the native build tools a from-source provider
  needs are on `PATH`.
- **`pkg`** ties all of the above together: given a project directory, it
  produces a fully resolved, installed build graph.

This is the piece that makes `importer`/`provider`/`lib` load-bearing rather
than merely parseable — nothing else in the toolchain decides *when* to
fetch, *where* things live on disk, or *whether* two projects can share an
install.

## Installation

```sh
go get github.com/vertex-language/pkg
```

## Quick start

```go
package main

import (
	"log"

	"github.com/vertex-language/pkg"
	"github.com/vertex-language/pkg/importer"
)

func main() {
	homeDir, err := pkg.Home("") // "" = no -vertex-home override
	if err != nil {
		log.Fatal(err)
	}

	cache, err := pkg.OpenCache(homeDir, importer.NewGitFetcher())
	if err != nil {
		log.Fatal(err)
	}

	graph, err := pkg.Load(".", cache, pkg.ModReadonly)
	if err != nil {
		log.Fatal(err)
	}

	for _, m := range graph.Modules {
		log.Printf("%s@%s  (%s)", m.Path, m.Version, m.Dir)
	}

	libs, err := graph.EnsureNativeLibs(cache, "amd64", "linux", "22.04", nil)
	if err != nil {
		log.Fatal(err)
	}
	for _, l := range libs {
		log.Printf("native lib for %s installed at %s", l.Module.Path, l.Dir)
	}
}
```

## `$VERTEX_HOME` and the cache layout

`Home` resolves `$VERTEX_HOME`'s effective value with the same precedence
Go uses for `GOPATH`/`GOMODCACHE`:

```go
func Home(override string) (string, error)
```

```
1. override (typically a -vertex-home CLI flag)   — highest priority
2. $VERTEX_HOME environment variable
3. ~/.vertex                                       — default
```

`Home` is the *only* place in this package that reads the environment.
`OpenCache` and everything else take the resolved directory explicitly, so
tests (and any future embedder — an LSP, a build-system integration) can
point a `Cache` at a temp dir with no ambient state involved.

`OpenCache(homeDir, fetcher)` creates, if needed, the cache rooted at
`homeDir/cache`:

```
$VERTEX_HOME/
  cache/
    download/            reserved for raw fetch caching (not yet used —
                          importer.Fetcher.Fetch currently writes straight
                          into cache/mod's scratch dirs)
    mod/
      <module-path>@<version>/    extracted module source, read-only
      .tmp-*/                     in-progress extraction, renamed into
                                   place atomically on success
    lock/
      mod-<key>.lock               per (module path, version) file lock
      lib-<key>.lock               per resolved-artifact file lock
    lib/
      <hash>/                     shared native-lib install, keyed by the
                                   resolved artifact (see below)
        bin/ lib/ include/        same layout provider.Provider.Install
                                   already writes, just relocated here
```

Everything under `cache/` is shared, read-write, across every project on
the machine and safe for concurrent `vertex` invocations — see **Locking**
below.

## `vs.sum` and module verification

`Cache.Mod` is what turns a `(module path, version)` pair into a directory
on disk:

```go
func (c *Cache) Mod(path mod.ModulePath, version string, sumPath string, mode LoadMode) (dir string, err error)
```

`sumPath` is a project's `vs.sum` — a flat, sorted, `go.sum`-shaped file:

```
github.com/someuser/yourpackage v1.2.3 h1:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08
```

Unlike `go.sum`, there is no separate `/go.mod`-hash line — a `vs.mod` file
is never fetched independently of its module's source, so one hash per
module is enough. The hash covers every regular file under the extracted
module tree: each file is hashed individually, then the sorted list of
`<hex sha256>  <path>` lines is itself hashed (`hashDir`, `dirhash.Hash1`-
style) — deterministic across extraction order, file mode, and OS path
separators.

`Mod` re-verifies a cache hit against `sumPath` on *every* call, not just
on first fetch: `vs.sum` is what a clone of the project ships and checks,
the cache directory's mere existence proves nothing about *this* project's
trust in it (some other project may have populated it).

### `LoadMode`

```go
const (
	ModReadonly LoadMode = iota // default
	ModUpdate
)
```

- **`ModReadonly`** never fetches a version with no existing `vs.sum` entry
  and never writes `vs.sum`. This is the default for `vertex build`/`run` —
  matching modern Go's own default — because a shared, machine-wide cache
  is exactly the place where a plain build silently mutating `vs.sum` or
  pulling a new upstream version is the wrong behavior.
- **`ModUpdate`** allows resolving and recording new entries — for
  `vertex mod get`/`tidy`.

An entry that already exists in `vs.sum` is always verified regardless of
mode; `mode` only gates *adding new trust*, never skips checking existing
trust.

## Resolving a dependency graph

```go
func Load(rootDir string, c *Cache, mode LoadMode) (*Graph, error)

type Module struct {
	Path    mod.ModulePath
	Version string     // "" for the root module and filesystem replace targets
	Dir     string
	ModFile *mod.File
	LibFile *lib.File  // nil if the module carries no vs.lib
}

type Graph struct {
	Root    *Module
	Modules []*Module  // topological order: dependencies before dependents
}
```

`Load` reads `rootDir`'s `vs.mod`, walks its `Dependencies` transitively
(fetching each via `c.Mod`), and returns the result in build order —
`Modules[i]` never depends on any `Modules[j]` where `j > i`, so a caller
(`driver`) can lower each module in list order and thread the results
forward.

`Replace` and `Exclude` are read once, from the **root** module's `vs.mod`
only, and applied while walking — same scoping Go gives `replace`/`exclude`
in `go.mod`. A `replace ... => ./local/dir` target is read directly from
disk, bypassing the cache and `vs.sum` entirely: it's local, actively-edited
source, the same reason Go's own filesystem replaces never touch `go.sum`.

Each dependency's own `vs.lib`, if it has one, is parsed with `lib.ParseLax`
rather than `lib.Parse` — a dependency was already validated by its own
author's toolchain, so an unrecognized field there shouldn't break every
downstream project.

### Known gaps

Flagged explicitly rather than silently under-handled, same spirit as
`mod.IsValidModulePath`'s and `importer`'s documented gaps:

- **No minimal version selection.** If two dependencies in the graph
  require different versions of the same module, `Load` fails with an
  explicit conflict error naming both requirers, rather than picking the
  higher one. Resolve it with a `replace` directive until MVS is
  implemented.
- **Versioned `replace` is treated as blanket.** A `replace old/path
  v1.0.0 => ...` line currently rewrites *every* requirement of
  `old/path`, not just the exact version Go's own semantics would target.
- **`exclude`/`replace` precedence is simplified.** `exclude` is checked
  against the originally required `(path, version)` before `replace` is
  applied; Go's actual precedence among exclude, replace, and MVS is more
  subtle than this and will need revisiting alongside real MVS support.
- **Module paths aren't case-escaped** the way Go's module cache escapes
  them, so two paths differing only in case could collide on a
  case-insensitive filesystem.

## Native libraries

```go
func (g *Graph) EnsureNativeLibs(c *Cache, arch, osTag, hostRelease string, logger provider.Logger) ([]LibResult, error)

func (c *Cache) LibInstall(lf *lib.File, pv *lib.Provider, t *lib.Target, logger provider.Logger) (dir string, err error)

type LibResult struct {
	Module *Module
	Dir    string
}
```

`EnsureNativeLibs` walks every `Module` in a graph, resolves any `vs.lib`
it carries via `lib.File.Resolve` for the given host profile, and installs
the result through `LibInstall` — so the compiler driver only ever asks the
`Graph` for library search directories; it never talks to `provider` or
`toolchain` directly.

`LibInstall`'s cache key hashes only the fields that determine what
actually gets installed — provider kind, resolved `package`/`url`/
`format`/`hash`/`lib`/`vcpkg_triplet`, and the `vs.lib` file's own
`version` — never the module path or version that happened to reference
it. This is what makes two unrelated modules requiring the identical
artifact at the identical version share a single install automatically,
the same sharing guarantee the module cache gives source code.

### Provider coverage

| `lib.Kind` | Status |
| --- | --- |
| `fetch` | Implemented directly in `pkg` (raw download + hash verification + zip/tar.gz extraction) — there's no OS-specific behavior to normalize, so it doesn't route through `pkg/provider` at all. |
| `apt`, `brew`, `vcpkg` | **Not yet implemented.** Wiring these through requires mapping a `vs.lib` `Target`'s `(OS, Release)` into each provider's own `Platform` string syntax (`apt`: `"ubuntu:22.04"`; `brew`: `"linux"`/`"linux:arm64"`; `vcpkg`: no `Platform` at all — arch + `VcpkgTriplet` directly). That mapping needs its own design pass rather than a guessed transform. |
| `dnf`, `pacman` | Recognized by `pkg/lib`'s grammar but have no `pkg/provider` implementation to call at all yet. |

- **`fetch` archive support** currently covers `zip` and `tar.gz`/`tgz`
  only — `bz2`/`xz`/`zst` are not yet wired up here, even though
  `pkg/provider/apt` already supports all three for `.deb` unpacking. This
  is a good candidate to factor into one shared archive helper both
  packages use, rather than duplicating decompressor selection twice.

## Locking

Every mutating operation — `Cache.Mod`, `Cache.LibInstall` — acquires a
per-key, cross-process file lock (via `flock`) around its full
check-then-fetch-then-extract sequence:

- `Cache.Mod` locks on `(module path, version)`.
- `Cache.LibInstall` locks on the resolved-artifact hash described above.

This is what makes two `vertex build` invocations racing to populate the
same cache entry block on each other rather than extracting into, or
installing over, the same directory concurrently. Locks are scoped as
narrowly as the key allows: two different versions of the same module, or
two different native-library artifacts, never contend with each other.

## Errors

Like every other package in this toolchain, `pkg` defines no custom error
type — failures are plain `error`, `%w`-wrapped back through `fmt.Errorf`
from whichever underlying operation failed (a hash mismatch, a missing
`vs.mod`, an `importer`/`provider` failure, a lock acquisition failure), so
`errors.Is`/`errors.As` reach whatever sentinel the wrapped layer provides.

## License

MIT