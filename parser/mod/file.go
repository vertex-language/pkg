package mod

// File is the parsed, interpreted form of a vs.mod file.
type File struct {
	Module       *Module
	Vertex       *Vertex // vs.mod's compiler-version directive, renamed per vs.lib §2
	Toolchain    *Toolchain
	Dependencies []*Dependency
	Exclude      []*Exclude
	Replace      []*Replace
	Retract      []*Retract
	Tool         []*Tool
	Ignore       []*Ignore
	Syntax       *FileSyntax
}

// ModulePath is a Vertex module path: slash-separated elements, no
// leading/trailing slash or dot per element, leading element
// conventionally a domain.
type ModulePath string

// ModuleVersion is a (path, version) pair, as it appears in
// dependencies, exclude, and replace directives.
type ModuleVersion struct {
	Path    ModulePath
	Version string
}

// Module is the "module" directive: the main module's own path.
// Exactly one must appear in a valid vs.mod file.
type Module struct {
	Path       ModulePath
	Deprecated string // set from a "Deprecated:" comment paragraph, if present
	Syntax     *Line
}

// Vertex is vs.mod's compiler-version directive, per vs.lib §2's
// naming. Sets the minimum Vertex compiler version a module was
// written against.
type Vertex struct {
	Version string
	Syntax  *Line
}

// Toolchain names a suggested compiler toolchain.
type Toolchain struct {
	Name   string
	Syntax *Line
}

// Dependency declares a minimum required version of a dependency,
// under vs.mod's "dependencies" keyword.
type Dependency struct {
	Mod      ModuleVersion
	Indirect bool // has a "// indirect" suffix comment
	Syntax   *Line
}

// Exclude prevents a specific module version from being selected.
// Applies only in the main module's vs.mod.
type Exclude struct {
	Mod    ModuleVersion
	Syntax *Line
}

// Replace substitutes the contents of one module version (or all
// versions) with another module version or a local directory path.
type Replace struct {
	Old    ModuleVersion
	New    ModuleVersion // New.Version == "" when New.Path is a filesystem path
	Syntax *Line
}

// VersionInterval is a closed [Low, High] range; Low == High for a
// single retracted version.
type VersionInterval struct {
	Low, High string
}

// Retract marks a version or version range as one that should not
// be depended upon.
type Retract struct {
	VersionInterval
	Rationale string // from a comment on or above the retract line
	Syntax    *Line
}

// Tool adds a package as a dependency and makes it runnable.
type Tool struct {
	Path   ModulePath
	Syntax *Line
}

// Ignore excludes a directory tree from package-pattern matching.
type Ignore struct {
	Path   string // slash-separated relative path
	Syntax *Line
}