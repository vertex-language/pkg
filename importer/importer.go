// Package importer fetches ordinary Vertex packages — plain git-hosted
// modules described by a vs.mod file — as opposed to the native-library
// providers in pkg/provider, which pkg/lib drives separately.
//
// importer has no opinion about where fetched content lives on disk;
// that's pkg.Cache's job (layout, locking, vs.sum verification). importer
// only knows how to turn (module path, version query) into a canonical
// version, and (module path, version, destination dir) into checked-out
// source at that exact commit.
package importer

import "github.com/vertex-language/pkg/parser/mod"

// Fetcher resolves and materializes a Vertex module from its VCS host.
type Fetcher interface {
	// List returns every canonical version (a git tag matching vX.Y.Z)
	// available for path, ascending. Branches and untagged commits are
	// never included — use Resolve for those.
	List(path mod.ModulePath) ([]string, error)

	// Resolve turns a version query into a canonical version string:
	//
	//   - "" or "latest" -> the highest tag from List, or a pseudo-version
	//     for the default branch's HEAD if the module has no tags at all.
	//   - an existing tag  -> itself, unchanged.
	//   - a branch name    -> a pseudo-version for that branch's HEAD.
	//   - a full 40-char commit hash -> a pseudo-version for that commit.
	//
	// The returned string is always a value Fetch can accept. This is
	// also the shape mod.VersionFixer expects, modulo the extra
	// module-path argument mod.Parse's fix callback already carries.
	Resolve(path mod.ModulePath, query string) (version string, err error)

	// Fetch checks out path@version into dir, which must already exist
	// and be empty. version must be a value Resolve returned for this
	// path — a real tag or a pseudo-version — not an arbitrary ref, so
	// callers always cache under a name that uniquely identifies the
	// content they got.
	Fetch(path mod.ModulePath, version string, dir string) error
}

// NewGitFetcher returns a Fetcher that talks to module paths' git remotes
// directly over HTTPS. It holds no local state or cache of its own —
// pkg.Cache owns the on-disk layout Fetch writes into.
func NewGitFetcher() Fetcher { return gitFetcher{} }