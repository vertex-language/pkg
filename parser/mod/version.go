package mod

import "regexp"

// VersionFixer canonicalizes a (module path, version) pair seen
// while parsing — e.g. resolving a branch name to a pseudo-version.
// A nil VersionFixer requires every version already be canonical.
type VersionFixer func(path, version string) (string, error)

// vertexVersionRE matches a release version optionally followed by
// a pre-release tag, e.g. "1.0", "1.2.3", "1.21rc1".
var vertexVersionRE = regexp.MustCompile(
	`^([1-9][0-9]*)\.(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*))?([a-z]+[0-9]+)?$`,
)

// IsValidVertexVersion reports whether s is a well-formed argument
// to the "vertex" directive.
func IsValidVertexVersion(s string) bool {
	return vertexVersionRE.MatchString(s)
}

// canonicalVersionRE matches a canonical semantic version: "v"
// followed by SemVer 2.0.0, the shape required outside the main
// module.
var canonicalVersionRE = regexp.MustCompile(
	`^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`,
)

// IsCanonicalVersion reports whether s is a canonical version string.
func IsCanonicalVersion(s string) bool { return canonicalVersionRE.MatchString(s) }

// modulePathRE is a practical (not exhaustive) check on module path
// shape: non-empty slash-separated elements, no leading/trailing
// slash. Full Windows-reserved-name and tilde-suffix checks from the
// upstream spec are deliberately not reproduced here — flagging that
// gap rather than silently under-validating.
var modulePathRE = regexp.MustCompile(`^[^/](.*[^/])?$`)

// IsValidModulePath reports whether s could be a Vertex module path.
func IsValidModulePath(s string) bool {
	if s == "" {
		return false
	}
	return modulePathRE.MatchString(s)
}