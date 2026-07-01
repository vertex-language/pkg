package importer

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const pseudoTimeLayout = "20060102150405"

// pseudoVersionRE matches this package's pseudo-version shape:
// v0.0.0-<14-digit UTC timestamp>-<40-hex commit hash>.
//
// This deviates from Go's own pseudo-version format, which truncates the
// hash to 12 hex characters. Go can afford that because its module proxy
// holds full repo history and disambiguates a short hash against it; we
// have no proxy, so Fetch needs to ask the remote for an exact object id
// directly, which means keeping the full hash.
//
// Known gap: like Go's pseudo-versions, this never incorporates the
// nearest preceding tag as a base — it's always "v0.0.0-...", so a
// pseudo-version from a repo with real tags will sort *before* those
// tags regardless of how recent the commit actually is. A module with
// commits it wants ordered correctly against its own tags should tag
// them.
var pseudoVersionRE = regexp.MustCompile(`^v0\.0\.0-(\d{14})-([0-9a-f]{40})$`)

func pseudoVersion(t time.Time, commitHash string) string {
	return fmt.Sprintf("v0.0.0-%s-%s", t.UTC().Format(pseudoTimeLayout), commitHash)
}

// pseudoVersionHash returns the full commit hash embedded in a
// pseudo-version, and false if v isn't one.
func pseudoVersionHash(v string) (string, bool) {
	m := pseudoVersionRE.FindStringSubmatch(v)
	if m == nil {
		return "", false
	}
	return m[2], true
}

// isCanonicalVersion reports whether s has the shape "v"+SemVer 2.0.0 —
// the same check mod.IsCanonicalVersion makes. Duplicated rather than
// imported: importer only needs the shape, not any of vs.mod's directive
// vocabulary, and shouldn't otherwise depend on package mod beyond the
// ModulePath type.
var canonicalVersionRE = regexp.MustCompile(
	`^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`,
)

func isCanonicalVersion(s string) bool { return canonicalVersionRE.MatchString(s) }

// isFullHash reports whether s is a complete 40-character hex commit
// hash. Abbreviated hashes aren't resolved — same limitation git itself
// has without a local object database to disambiguate against — so
// Resolve only accepts the full form.
func isFullHash(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// sortVersions sorts canonical versions ascending by SemVer precedence.
// Callers must filter to isCanonicalVersion strings first.
func sortVersions(vs []string) {
	sort.Slice(vs, func(i, j int) bool { return versionLess(vs[i], vs[j]) })
}

func versionLess(a, b string) bool {
	na, preA := splitSemver(a)
	nb, preB := splitSemver(b)
	for k := 0; k < 3; k++ {
		if na[k] != nb[k] {
			return na[k] < nb[k]
		}
	}
	switch {
	case preA == "" && preB == "":
		return false
	case preA == "":
		return false // a release outranks any prerelease of the same core version
	case preB == "":
		return true
	default:
		return preA < preB
	}
}

func splitSemver(v string) (core [3]int, prerelease string) {
	body := strings.TrimPrefix(v, "v")
	if i := strings.IndexByte(body, '+'); i >= 0 {
		body = body[:i] // build metadata never affects ordering
	}
	if i := strings.IndexByte(body, '-'); i >= 0 {
		prerelease = body[i+1:]
		body = body[:i]
	}
	parts := strings.SplitN(body, ".", 3)
	for i := 0; i < 3 && i < len(parts); i++ {
		n, _ := strconv.Atoi(parts[i])
		core[i] = n
	}
	return core, prerelease
}

func errf(format string, args ...any) error { return fmt.Errorf("importer: "+format, args...) }