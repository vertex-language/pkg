# lib

Package `lib` implements a parser and formatter for `vs.lib` files — the native-library provider manifest consumed alongside `vs.mod` by the Vertex toolchain.

```go
import "github.com/vertex-language/pkg/parser/lib"
```

## Overview

A `vs.lib` file tells the Vertex toolchain how to obtain and link a native library across platforms: one or more `provider` blocks (`apt`, `dnf`, `pacman`, `brew`, `vcpkg`, `fetch`, or a custom kind), each holding one or more `target` blocks scoped to an `<arch>-<os>` tag and, optionally, a specific host release.

The package is split into three layers, matching the sibling `mod` package:

- **Lexing/parsing** (`lex.go`, `parse.go`, `syntax.go`) — tokenizes a `vs.lib` file into an uninterpreted `*FileSyntax` tree, then interprets it into typed fields on a `*File`.
- **Interpreted model** (`file.go`) — the typed view (`File`, `Provider`, `Target`, ...) that most callers work with, including host resolution.
- **Formatting** (`print.go`) — renders a `*FileSyntax` tree back to canonical `vs.lib` text, including comments.

`vs.lib` files are typically read alongside a `vs.mod` file for the same module; see the `mod` package for that format.

## Installation

```sh
go get github.com/vertex-language/pkg/parser/lib
```

## Quick start

```go
package main

import (
	"fmt"
	"os"

	"github.com/vertex-language/pkg/parser/lib"
)

func main() {
	data, err := os.ReadFile("vs.lib")
	if err != nil {
		panic(err)
	}

	f, err := lib.Parse("vs.lib", data)
	if err != nil {
		panic(err)
	}

	fmt.Println("library:", f.Library.Path)
	fmt.Println("name:   ", f.Library.Name)
	fmt.Println("version:", f.Version.Value)

	for _, pv := range f.Providers {
		fmt.Printf("provider %s (%d targets)\n", pv.Kind, len(pv.Targets))
	}

	// Pick the provider/target this host would use.
	pv, t, err := f.Resolve("amd64", "linux", "22.04")
	if err != nil {
		panic(err)
	}
	fmt.Printf("resolved: provider=%s target=%s -l%s\n",
		pv.Kind, t.Tag, t.LinkOrDefault(f.Library.Name))
}
```

Round-tripping a parsed file back to text:

```go
out := lib.Format(f.Syntax)
os.WriteFile("vs.lib", out, 0o644)
```

Because `Format` operates on the `*FileSyntax` tree rather than the interpreted `*File`, it preserves the original comments and structure, aside from re-quoting `Field` values through `AutoQuote` (the `library` line itself is never quoted — see below).

## Parsing: `Parse` vs `ParseLax`

```go
func Parse(filename string, data []byte) (*File, error)
func ParseLax(filename string, data []byte) (*File, error)
```

Both parse a `vs.lib` file and return an interpreted `*File`. The difference is how an unrecognized top-level directive or an unrecognized field inside a `provider`/`target` block is handled:

- `Parse` treats it as a hard error.
- `ParseLax` silently ignores it, useful when reading a `vs.lib` that may target a newer compiler than the one doing the parsing.

Every `*File` returned by either function must have exactly one `library` directive and exactly one `version` directive, the latter non-empty — their absence is always an error, even under `ParseLax`. An unrecognized `provider` **kind**, unlike an unrecognized directive/field, always parses successfully under both functions — `Provider.Kind` is kept verbatim — it simply receives no built-in OS validation or resolver support.

## File format

```
library     github.com/username/widget
version     = "1.0.0"
description = "Native widget rendering library"

provider apt {
	package = "libwidget-dev"
	lib = "libwidget.so"

	target "amd64-linux" release "ubuntu-22.04" {
		hash = "h1:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	}
	target "amd64-linux" {
		hash = "h1:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	}
}

provider fetch {
	url = "https://example.com/widget-1.0.0.tar.gz"
	format = "tar.gz"
	hash = "h1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	lib = "libwidget.a"

	target "amd64-linux" {}
	target "arm64-darwin" {}
}
```

`library` is unquoted and bare — like `vs.mod`'s `module` line, not like the quoted `Field`s below it. Meta directives (`library`, `version`, `description`) must all appear before any `provider` block, and within a `provider` block its fields must all appear before its `target` blocks.

## Directive reference

| Directive | Field(s) | Notes |
| --- | --- | --- |
| `library <import-path>` | `File.Library` | Required. Bare, unquoted import path — e.g. `library github.com/username/widget`. `File.Library.Name` is derived from the path's final segment and is the linker fallback (§6) when `link` is never set anywhere. |
| `version` | `File.Version` | Required, non-empty. |
| `description` | `File.Description` | Optional. |
| `provider Kind { ... }` | `File.Providers` | `Kind` is stored verbatim, even if unrecognized. |
| `target "tag" [release "r"] { ... }` | `Provider.Targets` | `tag` has the form `<arch>-<os>`; the optional `release "r"` qualifies the target to a host release. |

There's no separate `name` field: the canonical name always comes from `library`'s import path, so there's nothing to keep in sync between the two.

### Provider/target fields

| Field | Meaning | Defaulting (Rule 1) | Notes |
| --- | --- | --- | --- |
| `link` | `-l<name>` linker argument | provider → target | falls back to `File.Library.Name` (derived from `library`) if unset everywhere (§6) |
| `package` | package name | provider → target | only meaningful under `apt`/`dnf`/`pacman`/`brew` |
| `url` | source URL | provider → target | provider-level `url` left un-overridden marks the target as sharing that artifact (Rule 2) |
| `format` | archive format | provider → target | only meaningful under `fetch` |
| `hash` | `h1:<sha256-hex>` pin | **never defaulted** (Rule 2) | see below |
| `lib` | library file to locate/link | provider → target | matched per Rule 3 |
| `vcpkg_triplet` | vcpkg triplet override | target only | only meaningful under `vcpkg`; a provider-level `vcpkg_triplet` is a hard error |

## Provider kinds (§5)

| Kind | Constant | Valid target OS |
| --- | --- | --- |
| `apt` | `KindApt` | `linux` |
| `dnf` | `KindDnf` | `linux` |
| `pacman` | `KindPacman` | `linux` |
| `brew` | `KindBrew` | `darwin`, `linux` |
| `vcpkg` | `KindVcpkg` | `windows`, `linux`, `darwin` |
| `fetch` | `KindFetch` | `linux`, `darwin`, `windows` |

An unrecognized kind isn't checked against this table at all (Rule 5 only applies to recognized kinds).

## Validation rules (Rules 1–5)

`Parse`/`ParseLax` run `validateProvider` on every provider block:

- **Rule 1 — provider-level defaults.** `link`, `package`, `url`, `format`, and `lib` set on a provider flow down to any target that doesn't set its own.
- **Rule 2 — hash and shared artifacts.** `hash` only makes sense alongside a `url`, and is never inherited. A provider-level `url` left un-overridden by a target makes that target's artifact *shared*: its hash lives at the provider level and the target must not declare its own `hash`. Otherwise the target owns its artifact and must declare its own valid `hash`. A provider with any shared-artifact target must itself declare a valid `hash`.
- **Rule 3 — `lib` matching.** See `LibMatchMode` below.
- **Rule 4 — no release axis for shared artifacts.** A target sharing the provider-level artifact may not declare `release`.
- **Rule 5 — OS validity.** A target's OS must be valid for its provider's kind, per the table above.

A few further checks apply outside the numbered rules: `vcpkg` targets never take `release` at all (there's no host-release axis for vcpkg); `vcpkg_triplet` is only valid under a `vcpkg` provider; `package` is invalid under `fetch`/`vcpkg`; `format` is only valid under `fetch`; and every target must set `lib`.

## Hash format (§9.1)

```go
func IsValidHash(s string) bool
```

Reports whether `s` has the form `h1:` followed by exactly 64 lowercase hex characters.

## Target tags

```go
func IsValidTargetTag(s string) bool
```

Reports whether `s` has the form `<arch>-<os>`, with `arch` one of `amd64`/`arm64` and `os` one of `linux`/`darwin`/`windows`.

## Library matching (Rule 3)

```go
func (t *Target) LibMatchMode() LibMatchMode
```

Reports how `Target.Lib` should be matched:

- `LibMatchBasename` — no path separator in the value: matched as a unique basename anywhere in the extracted tree.
- `LibMatchPath` — contains a `/` or `\`: matched as an exact relative path.

## Resolution (§7)

```go
func (f *File) Resolve(arch, osTag, hostRelease string) (*Provider, *Target, error)
```

Picks the provider and target a build with the given host profile would use, walking `f.Providers` in file order:

1. A provider with no target for the requested `arch`-`os` is skipped.
2. Among that provider's matching targets, an exact `release` match wins.
3. If some matching targets declare `release` but none matches `hostRelease` and there's no unqualified catch-all, resolution fails immediately for that provider — it does **not** fall through to the next provider.
4. Otherwise, the unqualified catch-all target for that `arch`-`os` is used.
5. If no provider has any target for the requested `arch`-`os` at all, resolution fails.

## Syntax tree

For callers that need to edit and rewrite a `vs.lib` file while preserving formatting, `Parse`/`ParseLax` also populate `File.Syntax`, an uninterpreted `*FileSyntax`:

- `FileSyntax.Stmt` holds the file's top-level statements in source order — each a `*LibraryLine` (the `library` meta line), a `*FieldLine` (the `version`/`description` meta lines), or a `*ProviderBlock`.
- A `*ProviderBlock` holds its own `Fields []*FieldLine` and `Targets []*TargetBlock`; each `*TargetBlock` holds its `Fields []*FieldLine`.
- Every statement carries `Comments.Before` (whole-line comments immediately preceding it) and `Comments.Suffix` (an end-of-line comment on the same line), plus full `Position` (line, rune offset, byte offset) information via `Span()`.

`*LibraryLine` holds `Path`, the bare import path text (e.g. `"github.com/username/widget"`), with no quoting to strip — unlike `*FieldLine.Value`, which is the decoded contents of a quoted string.

`Format(fs *FileSyntax) []byte` walks this tree back into canonical text. `library`'s path is written bare, exactly as authored. Every `Field` value, by contrast, is always written as a quoted string literal — `AutoQuote` unconditionally wraps its input in `"..."`, escaping `\`, `"`, newlines (`\n`), and tabs (`\t`).

## Errors

Parse failures are returned as `*SyntaxError`:

```go
type SyntaxError struct {
	Filename string
	Pos      Position
	Err      error
}
```

`SyntaxError.Error()` formats as `file:line:col: message`, and `SyntaxError.Unwrap()` returns the underlying error, so `errors.Is`/`errors.As` work as expected.

## License

See the repository's top-level `LICENSE` file.