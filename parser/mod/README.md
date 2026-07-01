# mod

Package `mod` implements a parser and formatter for `vs.mod` files — the module
manifest consumed by the Vertex toolchain.

```go
import "github.com/vertex-language/pkg/parser/mod"
```

## Overview

`vs.mod`'s grammar is specified directly by **vs.lib §2**, and this package
tracks it directive-for-directive. Two directives are renamed relative to the
vocabulary you may be used to from other module-file formats:

| vs.mod keyword | Meaning |
| --- | --- |
| `vertex` | the module's own compiler-version line (vs.lib §2 calls this out explicitly) |
| `dependencies` | the dependency directive |

Every other keyword — `module`, `exclude`, `replace`, `retract`, `tool`,
`ignore`, `toolchain` — is standard module-file vocabulary and needed no
renaming.

The package is split into three layers:

- **Lexing/parsing** (`lex.go`, `parse.go`, `syntax.go`) — tokenizes a
  `vs.mod` file into an uninterpreted `*FileSyntax` tree, then interprets each
  directive into typed fields on a `*File`.
- **Interpreted model** (`file.go`) — the typed, directive-level view
  (`Module`, `Vertex`, `Dependency`, `Replace`, `Retract`, ...) that most
  callers work with.
- **Formatting** (`print.go`) — renders a `*FileSyntax` tree back to canonical
  `vs.mod` text, including comments.

## Installation

```sh
go get github.com/vertex-language/pkg/parser/mod
```

## Quick start

```go
package main

import (
	"fmt"
	"os"

	"github.com/vertex-language/pkg/parser/mod"
)

func main() {
	data, err := os.ReadFile("vs.mod")
	if err != nil {
		panic(err)
	}

	f, err := mod.Parse("vs.mod", data, nil)
	if err != nil {
		panic(err)
	}

	fmt.Println("module: ", f.Module.Path)
	if f.Vertex != nil {
		fmt.Println("vertex: ", f.Vertex.Version)
	}
	for _, dep := range f.Dependencies {
		fmt.Printf("dependency: %s %s (indirect=%v)\n",
			dep.Mod.Path, dep.Mod.Version, dep.Indirect)
	}
}
```

Round-tripping a parsed file back to text:

```go
out := mod.Format(f.Syntax)
os.WriteFile("vs.mod", out, 0o644)
```

Because `Format` operates on the `*FileSyntax` tree rather than the
interpreted `*File`, it preserves the original comments, blank lines, and
factored blocks (`dependencies ( ... )`) exactly as written, aside from
re-quoting tokens through `AutoQuote` where needed.

## Example `vs.mod`

```
module example.com/widget

vertex 1.21

toolchain vertex1.21.3

dependencies (
	example.com/foo v1.2.3
	example.com/bar v0.5.0 // indirect
)

exclude example.com/baz v1.0.0

replace example.com/foo => example.com/foo-fork v1.2.4

// published by mistake
retract v1.0.1

tool example.com/widget/cmd/widgetgen

ignore testdata
```

## Parsing: `Parse` vs `ParseLax`

```go
func Parse(filename string, data []byte, fix VersionFixer) (*File, error)
func ParseLax(filename string, data []byte, fix VersionFixer) (*File, error)
```

Both parse a `vs.mod` file and return an interpreted `*File`. The only
difference is how they handle a directive keyword they don't recognize:

- `Parse` treats an unknown directive as a hard error.
- `ParseLax` silently ignores unknown directives, which is useful when reading
  a `vs.mod` that may have been written against a newer compiler/toolchain
  than the one doing the parsing.

Every `*File` returned by either function is required to have exactly one
`module` directive; its absence is always an error, even under `ParseLax`.

### `VersionFixer`

```go
type VersionFixer func(path, version string) (string, error)
```

`fix`, if non-nil, is called for every `(path, version)` pair encountered
while parsing `dependencies`, `exclude`, and `replace` directives, and can be
used to canonicalize versions as they're read — for example, resolving a
branch or tag name to a canonical pseudo-version. A `nil` `VersionFixer`
requires that every version already be written in canonical form.

## Directive reference

| Directive | Field(s) on `File` | Notes |
| --- | --- | --- |
| `module` | `Module` | The main module's own path. Exactly one required. A `Deprecated:` comment paragraph above the line sets `Module.Deprecated`. |
| `vertex` | `Vertex` | Minimum Vertex compiler version. Must satisfy `IsValidVertexVersion`. |
| `toolchain` | `Toolchain` | Names a suggested compiler toolchain. |
| `dependencies` | `Dependencies []*Dependency` | `ModulePath Version`, optionally followed by a `// indirect` suffix comment, which sets `Dependency.Indirect`. May appear as a factored `dependencies ( ... )` block. |
| `exclude` | `Exclude []*Exclude` | Prevents a specific module version from being selected. Only meaningful in the main module's `vs.mod`. |
| `replace` | `Replace []*Replace` | `old/path [version] => new/path [version]` or `old/path [version] => ./local/dir`. A file-path target is any `New.Path` beginning with `.` or `/`, and never carries a `New.Version`. |
| `retract` | `Retract []*Retract` | Either a single version or a `[low, high]` range. `Retract.Rationale` is taken from a comment on or immediately above the line. |
| `tool` | `Tool []*Tool` | Adds a package as a dependency and marks it runnable. |
| `ignore` | `Ignore []*Ignore` | Excludes a slash-separated directory tree from package-pattern matching. |

## Syntax tree

For callers that need to edit and rewrite a `vs.mod` file while preserving
formatting, `Parse`/`ParseLax` also populate `File.Syntax`, an uninterpreted
`*FileSyntax`:

- `FileSyntax.Stmt` is the file's top-level directives in source order, each
  either a `*Line` (a bare directive) or a `*LineBlock` (a factored block like
  `dependencies ( ... )`).
- Every `*Line` and `*LineBlock` carries `Comments.Before` (whole-line
  comments immediately preceding it) and `Comments.Suffix` (end-of-line
  comment(s) following it on the same line), along with full `Position`
  (line, rune offset, byte offset) information for both start and end.

`Format(fs *FileSyntax) []byte` walks this tree back into canonical text,
quoting any token that needs it (via `AutoQuote`) — tokens containing spaces,
tabs, quotes, backticks, parens, or newlines are rendered as a double-quoted
string with `\` and `"` escaped.

## Errors

Parse failures are returned as `*SyntaxError`:

```go
type SyntaxError struct {
	Filename string
	Pos      Position
	Err      error
}
```

`SyntaxError.Error()` formats as `file:line:col: message`, and
`SyntaxError.Unwrap()` returns the underlying error, so `errors.Is`/`errors.As`
work as expected against sentinel or wrapped errors passed through a
`VersionFixer`.

## Validation helpers

```go
func IsValidVertexVersion(s string) bool // e.g. "1.0", "1.2.3", "1.21rc1"
func IsCanonicalVersion(s string) bool   // "v" + full SemVer 2.0.0
func IsValidModulePath(s string) bool
```

`IsValidModulePath` is a practical, non-exhaustive check on module path
shape (non-empty, slash-separated elements, no leading/trailing slash). It
does **not** reproduce the upstream spec's full Windows-reserved-name and
tilde-suffix checks — that's a known gap, called out in the source rather
than silently under-validated, so don't rely on it as the sole guard against
those cases.

## License

See the repository's top-level `LICENSE` file.