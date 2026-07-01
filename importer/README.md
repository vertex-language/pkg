# importer

Package `importer` fetches ordinary Vertex packages — plain git-hosted
modules described by a `vs.mod` file — as opposed to the native-library
providers in `pkg/provider`, which `pkg/lib` and `pkg` drive separately.

```go
import "github.com/vertex-language/pkg/importer"
```

## Overview

`importer` is deliberately narrow: it knows how to talk to a module path's
git remote and nothing else. It holds no on-disk cache, no lockfiles, no
`vs.sum`, and no knowledge of `$VERTEX_HOME` — that's `pkg.Cache`'s job
(see the top-level `pkg` package). `importer.Fetcher` turns a version
*query* into a canonical *version*, and a canonical version into checked-out
source at a directory the caller supplies.

```go
type Fetcher interface {
	List(path mod.ModulePath) ([]string, error)
	Resolve(path mod.ModulePath, query string) (version string, err error)
	Fetch(path mod.ModulePath, version string, dir string) error
}

func NewGitFetcher() Fetcher
```

`NewGitFetcher` talks to remotes directly over HTTPS via `go-git` — no
`git` binary required on the host, matching how `pkg/provider/vcpkg`
already uses `go-git` for its own clone. It holds no local state of its
own: `Fetch`'s `dir` argument is scratch space the caller owns and cleans
up, not a cache this package manages.

## Installation

```sh
go get github.com/vertex-language/pkg/importer
```

## Quick start

```go
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/vertex-language/pkg/importer"
	"github.com/vertex-language/pkg/mod"
)

func main() {
	f := importer.NewGitFetcher()
	path := mod.ModulePath("github.com/someuser/yourpackage")

	version, err := f.Resolve(path, "latest")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("resolved:", version)

	dir, err := os.MkdirTemp("", "vertex-fetch-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)

	if err := f.Fetch(path, version, dir); err != nil {
		log.Fatal(err)
	}
	fmt.Println("fetched into:", dir)
}
```

In normal use, nothing calls `Fetcher` directly like this — `pkg.Cache.Mod`
does, supplying its own managed cache directory in place of the temp dir
above and recording the result's content hash into a project's `vs.sum`.
`Resolve` alone is also what backs `vertex mod get`, since choosing *which*
version to add to `vs.mod` is the one place a version gets picked without
one already being pinned.

## Versions

A "version" `Fetch` accepts is always either:

- **A canonical tag** (`v1.2.3`), fetched as a shallow single-commit clone
  of that tag.
- **A pseudo-version** (`v0.0.0-<14-digit UTC timestamp>-<40-hex commit
  hash>`), for anything that isn't a canonical tag — a branch HEAD, a bare
  commit, or the default branch's HEAD when a module has no tags at all.
  `Fetch` recovers the exact commit from the embedded hash and checks it
  out directly, without following any ref.

This deviates from Go's own pseudo-version format, which truncates the
commit hash to 12 hex characters. Go can do that because its module proxy
holds full repository history and disambiguates a short hash against it;
without a proxy, the full hash is kept so `Fetch` can ask the remote for
that exact object directly, with nothing else to resolve an abbreviation
against.

`List` returns only real tags matching a canonical version shape, ascending
by SemVer precedence — branches and untagged commits are never included,
since `Resolve` is the entry point for those.

## `Resolve` semantics

| Query | Result |
| --- | --- |
| `""` or `"latest"` | Highest tag from `List`, or a pseudo-version at the default branch's HEAD if the module has no tags at all. |
| An existing tag (`v1.2.3`) | Itself, unchanged. |
| A branch name | A pseudo-version pinned at that branch's current HEAD commit. |
| A full 40-character commit hash | A pseudo-version wrapping that exact commit. |
| Anything else | An error — abbreviated hashes and unknown refs are not resolved. |

## Known gaps

Called out explicitly rather than silently under-handled, same spirit as
`mod.IsValidModulePath`'s documented gap:

- **No repo-root probing.** The full module path is always treated as the
  git remote itself. A monorepo-style path like
  `example.com/org/repo/subpkg` fails to clone rather than resolving to
  `example.com/org/repo` with `subpkg` as an in-repo subdirectory.
- **No preceding-tag base for pseudo-versions.** Every pseudo-version is
  `v0.0.0-...`, so it always sorts *before* any real tag on the same
  repo, regardless of how recent the underlying commit actually is.
- **Abbreviated commit hashes aren't resolved.** `Resolve` only accepts a
  full 40-character hash for a bare-commit query — the same limitation
  `git` itself has without a local object database to disambiguate
  against.
- **Commit-by-hash fetching requires host support** for
  `uploadpack.allowReachableSHA1InWant` (GitHub, GitLab, and Bitbucket
  Cloud all allow this for public repositories; a locked-down self-hosted
  server may not, and will surface as a plain fetch error rather than
  being special-cased).
- **Public repositories only.** `repoURL` builds a bare `https://` URL
  with no credential injection — there is currently no way to configure
  authentication for a private module path.

## Errors

Like every other package in this toolchain, `importer` defines no custom
error type — failures are plain `error`, wrapped with context via
`fmt.Errorf`, so `errors.Is`/`errors.As` still reach whatever `go-git`
returned underneath.

## License

See the repository's top-level `LICENSE` file.