// Package lib implements a parser and formatter for vs.lib files — the
// native-library provider manifest consumed alongside vs.mod by the
// Vertex toolchain, as specified by vs.lib's own spec document.
package lib

import "strings"

// File is the interpreted form of a vs.lib file.
type File struct {
	Library     *Library
	Version     *Version
	Description *Description

	Providers []*Provider

	Syntax *FileSyntax
}

// Library is the interpreted form of the `library <import-path>` line
// (§4). Name is derived, not authored: it's Path's final "/"-separated
// segment — the same convention vs.mod's package/module naming uses.
type Library struct {
	Path   string // full import path, e.g. "github.com/username/sqlite3"
	Name   string // canonical name, derived from Path (§4)
	Syntax *LibraryLine
}

type Version struct {
	Value  string
	Syntax *FieldLine
}

type Description struct {
	Value  string
	Syntax *FieldLine
}

// Recognized provider kinds (§5). An unrecognized Kind still parses
// (Provider.Kind holds it verbatim) but carries no built-in resolver
// or OS validation here — same deferral the grammar describes.
const (
	KindApt    = "apt"
	KindDnf    = "dnf"
	KindPacman = "pacman"
	KindBrew   = "brew"
	KindVcpkg  = "vcpkg"
	KindFetch  = "fetch"
)

var managedKinds = map[string]bool{KindApt: true, KindDnf: true, KindPacman: true, KindBrew: true}

// validOS is the third column of the §5 table: the OsTag values valid
// for each recognized kind.
var validOS = map[string][]string{
	KindApt:    {"linux"},
	KindDnf:    {"linux"},
	KindPacman: {"linux"},
	KindBrew:   {"darwin", "linux"},
	KindVcpkg:  {"windows", "linux", "darwin"},
	KindFetch:  {"linux", "darwin", "windows"},
}

// Provider is the interpreted form of a `provider Kind { ... }` block.
// Link/Package/URL/Format/Lib are provider-level defaults (Rule 1);
// Hash is the shared-artifact hash and only makes sense alongside URL
// (Rule 2) — it is never a default for targets.
type Provider struct {
	Kind string

	Link    string
	Package string
	URL     string
	Format  string
	Hash    string
	Lib     string

	Targets []*Target

	Syntax *ProviderBlock
}

// Target is the interpreted form of a `target "tag" [release "r"] { ... }`
// block, with provider-level defaults (Rule 1) already applied.
type Target struct {
	Tag     string
	Arch    string // "amd64" | "arm64"
	OS      string // "linux" | "darwin" | "windows"
	Release string // "" if unqualified

	Link         string
	Package      string
	URL          string
	Format       string
	Hash         string
	Lib          string
	VcpkgTriplet string

	Provider *Provider
	Syntax   *TargetBlock

	urlOwn bool // whether this target's own block set `url` (vs. inheriting it)
}

// LibMatchMode reports how Rule 3 matches Target.Lib.
type LibMatchMode int

const (
	LibMatchBasename LibMatchMode = iota // no path separator: unique basename anywhere in the tree
	LibMatchPath                         // contains a path separator: exact relative path
)

func (t *Target) LibMatchMode() LibMatchMode {
	if strings.ContainsAny(t.Lib, "/\\") {
		return LibMatchPath
	}
	return LibMatchBasename
}

// LinkOrDefault returns the effective -l<name> linker argument: the
// target's own or inherited `link`, or the vs.lib file's canonical
// library name (Library.Name, derived from the `library` import path)
// if link was never set anywhere (§6).
func (t *Target) LinkOrDefault(libraryName string) string {
	if t.Link != "" {
		return t.Link
	}
	return libraryName
}

// IsValidHash reports whether s is a well-formed `h1:<sha256-hex>`
// hash (§9.1).
func IsValidHash(s string) bool {
	const prefix = "h1:"
	if !strings.HasPrefix(s, prefix) {
		return false
	}
	hex := s[len(prefix):]
	if len(hex) != 64 {
		return false
	}
	for _, c := range hex {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// IsValidTargetTag reports whether s has the form "<arch>-<os>" with a
// recognized arch and OS.
func IsValidTargetTag(s string) bool {
	_, _, err := splitTag(s)
	return err == nil
}

func splitTag(tag string) (arch, osTag string, err error) {
	i := strings.IndexByte(tag, '-')
	if i < 0 {
		return "", "", errf("expected \"<arch>-<os>\"")
	}
	arch, osTag = tag[:i], tag[i+1:]
	switch arch {
	case "amd64", "arm64":
	default:
		return "", "", errf("unrecognized arch %q", arch)
	}
	switch osTag {
	case "linux", "darwin", "windows":
	default:
		return "", "", errf("unrecognized OS %q", osTag)
	}
	return arch, osTag, nil
}

// libraryName derives the canonical name (§4) from a `library` import
// path: its final "/"-separated segment.
func libraryName(path string) (string, error) {
	if path == "" {
		return "", errf("import path must not be empty")
	}
	segs := strings.Split(path, "/")
	last := segs[len(segs)-1]
	if last == "" {
		return "", errf("import path must not end in '/'")
	}
	return last, nil
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// ---- interpretation ----

func interpret(filename string, fs *FileSyntax, lax bool) (*File, error) {
	f := &File{Syntax: fs}
	seen := map[string]bool{}

	for _, stmt := range fs.Stmt {
		switch x := stmt.(type) {
		case *LibraryLine:
			if seen["library"] {
				return nil, syntaxErr(filename, x.Start, "repeated 'library' directive")
			}
			seen["library"] = true
			name, err := libraryName(x.Path)
			if err != nil {
				return nil, syntaxErr(filename, x.Start, "invalid 'library' path %q: %v", x.Path, err)
			}
			f.Library = &Library{Path: x.Path, Name: name, Syntax: x}
		case *FieldLine:
			switch x.Key {
			case "version":
				if seen["version"] {
					return nil, syntaxErr(filename, x.Start, "repeated 'version' directive")
				}
				seen["version"] = true
				f.Version = &Version{Value: x.Value, Syntax: x}
			case "description":
				if seen["description"] {
					return nil, syntaxErr(filename, x.Start, "repeated 'description' directive")
				}
				seen["description"] = true
				f.Description = &Description{Value: x.Value, Syntax: x}
			default:
				if !lax {
					return nil, syntaxErr(filename, x.Start, "unknown top-level directive %q", x.Key)
				}
			}
		case *ProviderBlock:
			pv, err := interpretProvider(filename, x, lax)
			if err != nil {
				return nil, err
			}
			f.Providers = append(f.Providers, pv)
		}
	}

	if f.Library == nil {
		return nil, syntaxErr(filename, Position{Line: 1, LineRune: 1}, "missing required 'library' directive")
	}
	if f.Version == nil {
		return nil, syntaxErr(filename, Position{Line: 1, LineRune: 1}, "missing required 'version' directive")
	}
	if f.Version.Value == "" {
		return nil, syntaxErr(filename, f.Version.Syntax.Start, "'version' must not be empty")
	}

	return f, nil
}

func interpretProvider(filename string, pb *ProviderBlock, lax bool) (*Provider, error) {
	pv := &Provider{Kind: pb.Kind, Syntax: pb}

	for _, f := range pb.Fields {
		switch f.Key {
		case "link":
			pv.Link = f.Value
		case "package":
			pv.Package = f.Value
		case "url":
			pv.URL = f.Value
		case "format":
			pv.Format = f.Value
		case "hash":
			pv.Hash = f.Value
		case "lib":
			pv.Lib = f.Value
		case "vcpkg_triplet":
			return nil, syntaxErr(filename, f.Start, "'vcpkg_triplet' is only valid on a target, not a provider default")
		default:
			if !lax {
				return nil, syntaxErr(filename, f.Start, "unknown field %q in provider %s", f.Key, pb.Kind)
			}
		}
	}

	for _, tb := range pb.Targets {
		t, err := interpretTarget(filename, tb, pv, lax)
		if err != nil {
			return nil, err
		}
		pv.Targets = append(pv.Targets, t)
	}

	if err := validateProvider(filename, pv); err != nil {
		return nil, err
	}
	return pv, nil
}

func interpretTarget(filename string, tb *TargetBlock, pv *Provider, lax bool) (*Target, error) {
	arch, osTag, err := splitTag(tb.Tag)
	if err != nil {
		return nil, syntaxErr(filename, tb.Start, "invalid target tag %q: %v", tb.Tag, err)
	}

	t := &Target{
		Tag: tb.Tag, Arch: arch, OS: osTag, Release: tb.Release,
		Provider: pv, Syntax: tb,
		// Rule 1: defaults flow down from the provider — except Hash.
		Link: pv.Link, Package: pv.Package, URL: pv.URL, Format: pv.Format, Lib: pv.Lib,
	}

	for _, f := range tb.Fields {
		switch f.Key {
		case "link":
			t.Link = f.Value
		case "package":
			t.Package = f.Value
		case "url":
			t.URL = f.Value
			t.urlOwn = true
		case "format":
			t.Format = f.Value
		case "hash":
			t.Hash = f.Value
		case "lib":
			t.Lib = f.Value
		case "vcpkg_triplet":
			t.VcpkgTriplet = f.Value
		default:
			if !lax {
				return nil, syntaxErr(filename, f.Start, "unknown field %q in target %q", f.Key, tb.Tag)
			}
		}
	}
	return t, nil
}

// validateProvider enforces Rules 1–5 (§5) across a provider and its
// targets.
func validateProvider(filename string, pv *Provider) error {
	if pv.Hash != "" && !IsValidHash(pv.Hash) {
		return syntaxErr(filename, pv.Syntax.Start, "provider %s: %q is not a valid hash (want \"h1:<64 lowercase hex chars>\")", pv.Kind, pv.Hash)
	}
	if pv.URL == "" && pv.Hash != "" {
		return syntaxErr(filename, pv.Syntax.Start, "provider %s: 'hash' at provider level requires a provider-level 'url' (Rule 2) — there is no shared artifact for it to pin", pv.Kind)
	}

	anyShared := false
	for _, t := range pv.Targets {
		shared := pv.URL != "" && !t.urlOwn // Rule 2: inherits the provider's url ⇒ shared artifact
		if shared {
			anyShared = true
			if t.Hash != "" {
				return syntaxErr(filename, t.Syntax.Start, "target %q may not declare its own 'hash': it uses the provider-level shared artifact, whose hash lives at provider level (Rule 2)", t.Tag)
			}
			if t.Release != "" {
				return syntaxErr(filename, t.Syntax.Start, "target %q may not declare 'release': a shared artifact cannot vary by release (Rule 4)", t.Tag)
			}
		} else {
			if t.Hash == "" {
				return syntaxErr(filename, t.Syntax.Start, "target %q: missing 'hash' for its own artifact (Rule 2)", t.Tag)
			}
			if !IsValidHash(t.Hash) {
				return syntaxErr(filename, t.Syntax.Start, "target %q: %q is not a valid hash (want \"h1:<64 lowercase hex chars>\")", t.Tag, t.Hash)
			}
		}

		if oss, ok := validOS[pv.Kind]; ok && !containsStr(oss, t.OS) {
			return syntaxErr(filename, t.Syntax.Start, "target %q: OS %q is not valid for provider kind %q (Rule 5)", t.Tag, t.OS, pv.Kind)
		}
		if pv.Kind == KindVcpkg && t.Release != "" {
			return syntaxErr(filename, t.Syntax.Start, "target %q: 'release' is not used with vcpkg (no host-release axis)", t.Tag)
		}
		if t.VcpkgTriplet != "" && pv.Kind != KindVcpkg {
			return syntaxErr(filename, t.Syntax.Start, "target %q: 'vcpkg_triplet' is only meaningful under a vcpkg provider", t.Tag)
		}
		if t.Package != "" && (pv.Kind == KindFetch || pv.Kind == KindVcpkg) {
			return syntaxErr(filename, t.Syntax.Start, "target %q: 'package' is only meaningful under a managed provider (apt/dnf/pacman/brew)", t.Tag)
		}
		if t.Format != "" && pv.Kind != KindFetch {
			return syntaxErr(filename, t.Syntax.Start, "target %q: 'format' is only meaningful under a fetch provider", t.Tag)
		}
		if t.Lib == "" {
			return syntaxErr(filename, t.Syntax.Start, "target %q: missing 'lib'", t.Tag)
		}
	}

	if anyShared && pv.Hash == "" {
		return syntaxErr(filename, pv.Syntax.Start, "provider %s: shared artifact (provider-level 'url') requires a provider-level 'hash'", pv.Kind)
	}
	return nil
}

// ---- resolution (§7) ----

// Resolve implements the §7 resolution order for a given build target
// (arch, os) and optional host release string, returning the provider
// and target a build with that host profile would select.
func (f *File) Resolve(arch, osTag, hostRelease string) (*Provider, *Target, error) {
	for _, pv := range f.Providers {
		var matching []*Target
		for _, t := range pv.Targets {
			if t.Arch == arch && t.OS == osTag {
				matching = append(matching, t)
			}
		}
		if len(matching) == 0 {
			continue // step 1: this provider has nothing for this arch-os; try the next
		}

		var qualified, catchAll *Target
		hasReleaseQualified := false
		for _, t := range matching {
			if t.Release == "" {
				catchAll = t
				continue
			}
			hasReleaseQualified = true
			if hostRelease != "" && t.Release == hostRelease {
				qualified = t
			}
		}
		if qualified != nil {
			return pv, qualified, nil // step 2: most specific by release
		}
		if hasReleaseQualified && catchAll == nil {
			// step 3: fails outright, no fallthrough to the next provider.
			return nil, nil, errf("provider %s: no target matches host release %q for %s-%s, and no unqualified catch-all is declared", pv.Kind, hostRelease, arch, osTag)
		}
		if catchAll != nil {
			return pv, catchAll, nil
		}
		return nil, nil, errf("provider %s: no target matches %s-%s for this host", pv.Kind, arch, osTag)
	}
	return nil, nil, errf("no provider has a target matching %s-%s", arch, osTag) // step 4
}

func errf(format string, args ...interface{}) error { return &simpleError{fmtErr(format, args...)} }

type simpleError struct{ s string }

func (e *simpleError) Error() string { return e.s }