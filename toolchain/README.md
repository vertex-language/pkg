# toolchain

Package `toolchain` makes sure the native build tools a from-source
provider needs (`cmake`, `ninja`, a compiler, ...) are present on the host,
installing any that are missing via whichever native provider owns the
current OS. It exists so that from-source providers like `vcpkg` never
need to import `apt`/`brew`/`winget` themselves.

```go
import "github.com/vertex-language/pkg/toolchain"
```

## Overview

`toolchain` sits above the four `provider` implementations rather than
beside them: it doesn't install packages for its own sake, it installs
whatever a *from-source* provider needs before that provider can start
compiling. `vcpkg` is the only current caller — it runs `EnsureBuildTools`
before every `Install` — but the package is written generically so any
future from-source provider can reuse it.

The core idea is a `Requirement`: a binary to probe for on `PATH`, plus a
map of which native package satisfies it, keyed by `GOOS`.

```go
type Requirement struct {
	Bin    string            // binary probed on PATH, e.g. "cmake"
	PkgFor map[string]string // GOOS -> package name; an absent OS is unsupported
}
```

`Ensure` walks a `[]Requirement`, and for each one whose `PkgFor` has an
entry for `runtime.GOOS`, checks whether `Bin` is already on `PATH` via
`exec.LookPath`. Anything missing gets installed through `apt` (linux),
`brew` (darwin), or `winget` (windows) — chosen purely by `runtime.GOOS`,
not by the target `Params.Platform` a caller may eventually build for.

## Installation

```sh
go get github.com/vertex-language/pkg/toolchain
```

## Quick start

```go
package main

import (
	"log"

	"github.com/vertex-language/pkg/toolchain"
)

func main() {
	// Ensure cmake, ninja, and a C/C++ compiler are on PATH, installing
	// any that are missing via the host's native provider.
	if err := toolchain.EnsureBuildTools("/opt/myenv", nil); err != nil {
		log.Fatal(err)
	}
}
```

This is exactly what `vcpkg.Vcpkg.Install` calls before compiling a port: "Getting a working build toolchain onto the host lives in pkg/toolchain, not here: this package only drives vcpkg itself once cmake/ninja/a compiler are already on PATH."

## `BuildTools`

```go
var BuildTools = []Requirement{
	{Bin: "cmake", PkgFor: map[string]string{
		"linux": "cmake", "darwin": "cmake", "windows": "Kitware.CMake",
	}},
	{Bin: "ninja", PkgFor: map[string]string{
		"linux": "ninja-build", "darwin": "ninja", "windows": "Ninja-build.Ninja",
	}},
	{Bin: "gcc", PkgFor: map[string]string{
		"linux": "gcc", "darwin": "gcc",
		// no windows entry — MSVC is expected via the VS Build Tools
		// installer; there's no winget package installed on its behalf.
	}},
	{Bin: "g++", PkgFor: map[string]string{
		"linux": "g++",
		// darwin's "gcc" formula already provides g++.
	}},
}
```

This is the requirement set for compiling C/C++ ports from source, and is
exported so other from-source providers besides `vcpkg` can reuse it
directly rather than redeclaring their own copy. Two things fall out of
how it's structured:

- **Windows has no `gcc`/`g++` entry.** MSVC is expected to already be
  present via the Visual Studio Build Tools installer; `toolchain` makes
  no attempt to install a compiler on Windows, only `cmake` and `ninja`.
- **`g++` has no `darwin` entry**, since Homebrew's `gcc` formula already
  provides `g++` — probing `g++` separately on macOS would be redundant,
  so it's simply left unsupported for that OS (an absent `GOOS` key means
  the requirement is skipped there, not that it errors).

## API

### `EnsureBuildTools`

```go
func EnsureBuildTools(envPath string, logger provider.Logger) error
```

Convenience wrapper: `Ensure(envPath, BuildTools, logger)`. This is what
`vcpkg` calls.

### `Ensure`

```go
func Ensure(envPath string, reqs []Requirement, logger provider.Logger) error
```

For each `Requirement` in `reqs`:

1. If `runtime.GOOS` has no entry in `PkgFor`, the requirement is silently
   skipped — it doesn't apply to this host.
2. Otherwise, `exec.LookPath(r.Bin)` checks whether the binary is already
   available. If it is, nothing happens for that requirement.
3. Anything still missing after that pass is collected into one `pkgs`
   list and installed in a single batch via `installNative`.

A `nil` logger is replaced with `provider.Noop`. If anything is missing,
`Ensure` fires one `Warn` listing every package about to be installed
before handing off to `installNative`. If nothing is missing, `Ensure`
returns `nil` without creating a provider or logging anything.

### `installNative`

Unexported; installs `pkgs` via whichever provider owns `runtime.GOOS`:

| `runtime.GOOS` | Provider used | 
| --- | --- |
| `linux` | `apt.New(envPath, logger)` |
| `darwin` | `brew.New(envPath, logger)` |
| `windows` | `winget.New(envPath, logger)` |
| anything else | not supported — returns an error naming the OS and the packages to install manually |

Each missing package is installed one at a time with zero-value
`Params{}` (no version constraint, no platform override, not
download-only) via that provider's own `Install`. A failure on any single
package aborts the loop and returns immediately — `Ensure` does not
partially retry or continue past the first failing install.

## Errors

Like `provider`'s four implementations, `toolchain` defines no custom
error type — every failure is a plain `error`, `%w`-wrapped back through
`fmt.Errorf` from either `provider.New`, `provider.Install`, or the
unsupported-OS case in `installNative`, so `errors.Is`/`errors.As` still
reach whatever the underlying provider wrapped.

## License

See the repository's top-level `LICENSE` file.