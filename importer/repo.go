package importer

import "github.com/vertex-language/pkg/parser/mod"

// repoURL derives the git remote URL for a module path.
//
// Known gap: unlike Go's module resolution, this never probes for a
// go-import meta tag or tries progressively shorter path prefixes as the
// repo root — the full module path is always treated as the repo itself.
// A monorepo-style module path like "example.com/org/repo/subpkg" will
// fail to clone rather than resolving to "example.com/org/repo" with
// "subpkg" as an in-repo subdirectory. Flagging this rather than
// silently mishandling it, same spirit as mod.IsValidModulePath's
// documented gaps.
func repoURL(path mod.ModulePath) (string, error) {
	if path == "" {
		return "", errf("empty module path")
	}
	return "https://" + string(path), nil
}