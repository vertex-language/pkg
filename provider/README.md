# provider

Package `provider` defines the shared `Provider` interface, `Params`, and
`Logger` contract implemented by every native package provider in this
module — `apt`, `brew`, `vcpkg`, and `winget` — plus those four concrete
implementations.

```go
import "github.com/vertex-language/pkg/provider"
```

## Overview

Each provider knows how to install and remove packages from exactly one
package ecosystem, and normalizes that ecosystem's own package/version/
platform vocabulary behind one interface:

```go
type Provider interface {
	Install(pkg string, params Params) error
	Remove(pkg string) error
	Resolve(pkg string) (string, error)
}
```

Four implementations satisfy `provider.Provider`:

| Package | Ecosystem | Ships prebuilt artifacts? | Needs a local toolchain? |
| --- | --- | --- | --- |
| `apt` | Debian/Ubuntu `.deb` | Yes | No |
| `brew` | Homebrew formulae/bottles | Yes | No |
| `vcpkg` | vcpkg ports (built from source) | No — compiles from source | Yes, via `toolchain` |
| `winget` | Windows Package Manager installers | Yes | No |

Every implementation is pure Go I/O against the ecosystem's public API or
mirror — none of them shells out to the ecosystem's own CLI (`apt-get`,
`brew`, `winget.exe`), so all four run on any host OS regardless of the
target platform. `vcpkg` is the one exception that execs anything at all: it
must run the upstream `bootstrap-vcpkg.sh`/`.bat` script once, and later
`vcpkg` itself, because there's no way to drive vcpkg without its own binary.

Each provider package aliases `provider.Params` and `provider.Logger`
locally (e.g. `apt.Params`, `apt.Logger`), so call sites don't need to
import `provider` directly just to construct one.

## Installation

```sh
go get github.com/vertex-language/pkg/provider
go get github.com/vertex-language/pkg/provider/apt
go get github.com/vertex-language/pkg/provider/brew
go get github.com/vertex-language/pkg/provider/vcpkg
go get github.com/vertex-language/pkg/provider/winget
```

## Quick start

```go
package main

import (
	"log"

	"github.com/vertex-language/pkg/provider"
	"github.com/vertex-language/pkg/provider/apt"
)

func main() {
	p, err := apt.New("/opt/myenv", nil) // nil logger = silent
	if err != nil {
		log.Fatal(err)
	}

	var _ provider.Provider = p // *apt.Apt satisfies the shared interface

	err = p.Install("libwidget-dev", provider.Params{
		Version:  "1.2",
		Platform: "ubuntu:22.04",
	})
	if err != nil {
		log.Fatal(err)
	}
}
```

`brew.New`, `vcpkg.New`, and `winget.New` all share this
`New(root string, logger Logger) (*T, error)` shape (vcpkg's first argument
is the environment path rather than a bin-relative dir, since it also owns
a clone and an installed-package tree under it).

## The shared contract

### `Params`

```go
type Params struct {
	Version      string // constraint/prefix understood by the provider; "" = latest
	Platform     string // target override, e.g. "debian:12", "macos:arm64"; "" = this host
	DownloadOnly bool   // fetch artifacts without installing/linking them
}
```

`Platform` syntax is provider-specific — see each provider's section below.

### `Logger`

```go
type Logger interface {
	DepsResolved(pkg string, preDeps, deps int)
	Downloading(name, version string, sizeBytes int64)
	DownloadProgress(name string, received, total int64)
	DownloadDone(name, version string)
	Installing(name, version string, isPre, isDep bool)
	Installed(name, version string, isPre, isDep bool)
	Warn(msg string)
}
```

One `Logger` implementation can be handed to `apt`, `brew`, `vcpkg`, and
`winget` simultaneously — every provider fires the same event sequence, so
a single progress UI can drive all four. `provider.Noop` is a no-op
`Logger`. Every `New` treats a `nil` logger safely: `apt`/`brew` store the
`nil` and guard each call site with `if a.logger != nil`, while
`vcpkg`/`winget` substitute `provider.Noop`.

`sizeBytes`/`total` are `-1` when the server doesn't report a content
length. `DownloadProgress` is throttled by each provider to roughly one
call per percentage point once size is known, or every read when it isn't.

## Provider implementations

### `apt`

```go
import "github.com/vertex-language/pkg/provider/apt"
```

Installs `.deb` packages by talking to a Debian/Ubuntu mirror directly — no
`apt-get`, no root, no chroot.

- **Platforms** (`Params.Platform`, default `debian:12`): `debian:11`,
  `debian:12`, `ubuntu:20.04`, `ubuntu:22.04`, `ubuntu:24.04`. Each maps to
  a codename, mirror URL, and arch.
- **Resolution**: fetches and gunzips the mirror's `Packages` index, finds
  the requested package (optionally constrained by a version prefix), then
  walks the full transitive dependency graph breadth-first —
  `Pre-Depends` first, then `Depends` — resolving `|`-separated
  alternatives and versioned constraints (`>=`, `<=`, `>>`, `<<`, `=`)
  against the Debian version-ordering algorithm (policy §5.6.12: epoch →
  upstream → revision). `Provides` entries register virtual package names.
  Packages marked `Essential` are assumed already present and dropped from
  the install plan.
- **Install order**: pre-dependencies, then regular dependencies, then the
  requested package.
- **Unpack**: a `.deb` is an `ar` archive; `data.tar.*` (gz/xz/bz2/zst) is
  extracted, keeping only entries under `usr/bin/`, `usr/lib/`,
  `usr/libexec/`, `usr/include/`, and `lib/`, with the full relative path
  preserved under the environment root.
- **DownloadOnly**: downloads the requested package and its full
  dependency closure without unpacking or installing anything.
- **Remove**: deletes `<envDir>/bin/<pkg>` — it does not reverse a full
  install (installed libs under `lib/`/`include/` are left in place).
- **Resolve**: passthrough (apt package names need no normalization).

### `brew`

```go
import "github.com/vertex-language/pkg/provider/brew"
```

Installs Homebrew bottles by talking to the Homebrew JSON API and
`ghcr.io` directly — no `brew` CLI required.

- **Platforms** (`Params.Platform`): `""` (host), `"macos"`/`"macos:arm64"`,
  `"macos:amd64"`, `"linux"`/`"linux:amd64"`, `"linux:arm64"`. Each maps to
  a bottle tag (e.g. `arm64_tahoe`, `x86_64_linux`).
- **Bottle fallback**: on macOS, if a formula has no bottle for the
  preferred (newest) tag, `brew` walks a fixed newest-first ladder
  (`tahoe` → `sequoia` → `sonoma` → `ventura` → `monterey` → `big_sur`, or
  their `arm64_` variants) until it finds one the formula actually ships.
- **Resolution**: fetches formula JSON from `formulae.brew.sh`, then walks
  `Dependencies` breadth-first, fetching each dependency's own formula
  individually so its transitives are picked up too. A dependency missing
  from the API (e.g. an OS-provided essential like glibc) is warned and
  skipped rather than failing the install.
- **Install order**: all resolved dependencies, then the requested
  formula.
- **Download**: bottle blobs live on `ghcr.io` and require a bearer token
  even for public images; `brew` uses Homebrew's documented anonymous
  token. Redirects to the CDN correctly drop the `Authorization` header
  (Go's default `http.Client` behavior on cross-host redirects).
- **Unpack**: extracts the bottle tar.gz into `Cellar/<pkg>/<kegVersion>/`
  (stripping the embedded `<pkg>/<kegVersion>/` prefix), then symlinks
  `bin/`, `sbin/`, `lib/`, `libexec/`, `include/`, and `share/` entries
  into the environment root — mirroring `brew link`. A versioned binary
  like `gcc-15` also gets an unversioned `gcc` alias if nothing already
  owns that name.
- **Remove**: removes every symlink under the linked directories that
  resolves into the formula's `Cellar` keg, then deletes the keg itself.
- **Resolve**: passthrough.

### `vcpkg`

```go
import "github.com/vertex-language/pkg/provider/vcpkg"
```

The one provider that builds from source rather than unpacking a prebuilt
artifact — and correspondingly the only one that execs external processes
or requires a real build toolchain (delegated to the sibling `toolchain`
package; see `toolchain/README.md`).

- **Bootstrap** (idempotent, run automatically before every
  `Install`/`Remove`): shallow-clones `microsoft/vcpkg` (`master`, depth
  1) via `go-git` if not already present, then runs the upstream
  `bootstrap-vcpkg.sh` (chmod'd `0755` first) or `bootstrap-vcpkg.bat` to
  produce the `vcpkg` binary.
- **Install modes**: no `Params.Version` runs classic mode
  (`vcpkg install pkg:triplet`); a version constraint switches to manifest
  mode — a temporary `vcpkg.json` (with a version `overrides` entry) plus
  a `vcpkg-configuration.json` pinning the default registry's baseline to
  the local clone's current `HEAD` commit — then
  `vcpkg install --x-manifest-root=<tmp>`.
- **Triplets**: `ResolveTriplet(platform)` maps `GOARCH` (`amd64`→`x64`,
  `arm64`→`arm64`, `386`→`x86`) and either the host `GOOS` or a platform
  string (`debian:*`/`ubuntu:*`→`linux`, `macos`→`osx`,
  `windows*`→`windows`) into a `<arch>-<os>` triplet, e.g. `x64-linux`.
- **Linking**: after install, tool binaries under
  `vcpkg_installed/<triplet>/tools/**` are hard-linked (falling back to a
  symlink across devices) into the environment's `bin/`.
- **Remove**: `vcpkg remove --recurse pkg:triplet`.
- **Resolve**: normalizes a package name to vcpkg's port-name convention —
  lowercase, underscores become hyphens, other non-alphanumeric characters
  dropped.
- **`Commit()` / `VcpkgCommit()`**: expose the short `HEAD` hash of the
  vcpkg clone (via `go-git`) so a caller can stamp a `vcpkg_commit` field
  into a lockfile for reproducible rebuilds. `VcpkgCommit()` returns `""`
  (not an error) if the environment has never bootstrapped.

### `winget`

```go
import "github.com/vertex-language/pkg/provider/winget"
```

Installs Windows Package Manager packages by reading manifests straight
out of the public `microsoft/winget-pkgs` GitHub repository — no
`winget.exe` or Windows APIs required, so it can run on any host OS
(though the installers it downloads are, of course, Windows binaries).

- **Package identifiers**: `Publisher.Package`, e.g. `Microsoft.PowerShell`,
  `Google.Chrome`.
- **Version resolution**: lists that package's version directories via the
  GitHub Contents API, then picks the highest version matching
  `Params.Version` as a prefix (or the highest overall when unset). An
  optional `GITHUB_TOKEN` environment variable raises the unauthenticated
  GitHub API rate limit.
- **Installer selection**: fetches and parses the version's
  `.installer.yaml`, then picks an `Installer` entry by architecture —
  exact match, then `neutral`, then the first listed.
- **Dependencies**: winget's manifest format exposes no transitive
  dependency graph, so `DepsResolved` always reports `0, 0`.
- **Download**: verifies `InstallerSha256` against the downloaded file
  when the manifest provides one, failing (and deleting the partial file)
  on mismatch.
- **Unpack**, by `InstallerType`:
  - `zip` — extracts `NestedInstallerFiles` if the manifest lists them,
    otherwise any `.exe`/`.dll`/`.so`/`.dylib` at any depth, into `bin/`.
  - `portable` — the download *is* the binary; placed in `bin/`, renamed
    to `PortableCommandAlias` if given.
  - `msix`/`msixbundle`/`appx`/`appxbundle` — these are ZIPs; top-level
    `.exe` entries are extracted to `bin/`.
  - anything else (`msi`, `exe`, `inno`, `nullsoft`, `wix`, `burn`, ...) —
    opaque; the installer is copied into `bin/` as-is, since actually
    running it needs the Windows installer subsystem.
- **Remove**: best-effort — deletes any file in `bin/` whose name (minus
  `.exe`) case-insensitively matches the package identifier's last
  dot-segment (`Microsoft.PowerShell` → `PowerShell`). There's no
  installed-file manifest to drive an exact removal.
- **Resolve**: passthrough (winget identifiers are already canonical).

## Errors

None of the four providers defines a package-specific error type; every
failure is a plain `error` produced with `fmt.Errorf` and `%w`-wrapping
back to the underlying cause (an HTTP status, a missing index entry, an
unsatisfied version constraint, an `exec.Cmd` failure, ...), so
`errors.Is`/`errors.As` work against whatever sentinel the wrapped layer
(e.g. `os`, `net/http`) provides.

## License

See the repository's top-level `LICENSE` file.