package pkg

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/vertex-language/pkg/parser/lib"
	"github.com/vertex-language/pkg/parser/mod"
)

// Module is one resolved node in a dependency graph: a fetched (or
// locally replaced) Vertex module already on disk, with its own vs.mod
// parsed, and its vs.lib parsed too if it has one.
type Module struct {
	Path    mod.ModulePath
	Version string // "" for the root module and for a filesystem replace target
	Dir     string
	ModFile *mod.File
	LibFile *lib.File // nil if this module carries no vs.lib
}

// Graph is a fully resolved dependency graph rooted at one project's
// vs.mod, topologically ordered so Modules[i] never depends on any
// Modules[j] where j > i. Root is also Modules' final element.
type Graph struct {
	Root    *Module
	Modules []*Module
}

// Load reads rootDir's vs.mod, resolves its full transitive dependency
// graph through c — fetching as needed, applying every Replace and
// Exclude directive — and returns it in build order.
//
// mode governs what Load is allowed to do, via c.Mod, about a
// dependency with no existing vs.sum entry: ModReadonly (the intended
// default for `vertex build`/`run`) fails closed; ModUpdate fetches and
// records it, for `vertex mod get`/`tidy`.
//
// Known gap: Load does not implement minimal version selection. If two
// dependencies in the graph require different versions of the same
// module, Load fails with an explicit conflict error rather than
// picking the higher one — resolve it with a replace directive until
// MVS lands.
//
// Load is a thin disk-backed front door onto LoadModule: it only reads
// and parses rootDir/vs.mod into a *mod.File and a rootDir/vs.sum path,
// then hands both off unchanged. Everything that actually resolves a
// graph lives in LoadModule and has never cared where its *mod.File
// argument came from.
func Load(rootDir string, c *Cache, mode LoadMode) (*Graph, error) {
	rootModPath := filepath.Join(rootDir, "vs.mod")
	rootData, err := os.ReadFile(rootModPath)
	if err != nil {
		return nil, fmt.Errorf("pkg: %w", err)
	}
	rootMF, err := mod.Parse(rootModPath, rootData, nil)
	if err != nil {
		return nil, err
	}
	return LoadModule(rootDir, filepath.Join(rootDir, "vs.sum"), rootMF, c, mode)
}

// LoadModule resolves rootMF's full transitive dependency graph through
// c, exactly as Load does, but takes an already-built *mod.File and an
// explicit sumPath instead of reading vs.mod off rootDir itself. Load is
// now just this function's disk-backed front door.
//
// This is the entry point for a root that has no vs.mod on disk at all
// — e.g. driver.Compile's single-file mode, which has no project-level
// vs.mod to read but still needs a real dependency graph for whatever
// imports the one file being compiled happens to name. That caller
// builds a minimal *mod.File by hand (a synthetic Module.Path, one
// Dependency per resolved import, no Replace/Exclude/etc. since there's
// no file for those directives to have come from) and points sumPath at
// a scratch file instead of a committed vs.sum, then drives the exact
// same resolveState walk a committed vs.mod would.
//
// rootDir is still required independent of sumPath: it's where the root
// package's own source lives, and where a filesystem replace target (if
// rootMF has any) resolves relative to. sumPath need not live under
// rootDir, and need not be a file the caller keeps around after this
// call returns.
func LoadModule(rootDir, sumPath string, rootMF *mod.File, c *Cache, mode LoadMode) (*Graph, error) {
	if rootMF.Module == nil {
		return nil, fmt.Errorf("pkg: root *mod.File has no Module directive")
	}

	rootLib, err := loadLibFile(rootDir)
	if err != nil {
		return nil, err
	}

	root := &Module{Path: rootMF.Module.Path, Dir: rootDir, ModFile: rootMF, LibFile: rootLib}

	rs := &resolveState{
		cache:    c,
		mode:     mode,
		rootDir:  rootDir,
		sumPath:  sumPath,
		replace:  indexReplace(rootMF.Replace),
		exclude:  indexExclude(rootMF.Exclude),
		visited:  map[mod.ModulePath]*Module{root.Path: root},
		visiting: map[mod.ModulePath]bool{root.Path: true},
	}

	if err := rs.walk(root); err != nil {
		return nil, err
	}
	return &Graph{Root: root, Modules: rs.order}, nil
}

// resolveState carries the bookkeeping one Load/LoadModule walk needs.
// replace and exclude are read once, from the root module's *mod.File
// only — those directives apply only in the main module, same as Go's
// go.mod.
type resolveState struct {
	cache   *Cache
	mode    LoadMode
	rootDir string
	sumPath string

	// Known gap: keyed by Old.Path only, so a *versioned* replace
	// ("replace old/path v1.0.0 => ...", which in Go only rewrites that
	// exact version) is treated the same as a blanket one. Every
	// replace here is effectively blanket until that distinction is
	// implemented.
	replace map[mod.ModulePath]mod.ModuleVersion
	exclude map[mod.ModuleVersion]bool

	visited  map[mod.ModulePath]*Module
	visiting map[mod.ModulePath]bool
	order    []*Module // post-order: a dependency is appended before its dependent
}

func (rs *resolveState) walk(m *Module) error {
	for _, dep := range m.ModFile.Dependencies {
		// exclude is checked against the originally required (path,
		// version), before replace is applied — Go's own precedence
		// between exclude/replace/MVS is more subtle than this
		// simplified version, pending real MVS support.
		if rs.exclude[dep.Mod] {
			return fmt.Errorf("pkg: %s requires %s@%s, which the main module's vs.mod excludes", m.Path, dep.Mod.Path, dep.Mod.Version)
		}

		mv := dep.Mod
		if r, ok := rs.replace[mv.Path]; ok {
			mv = r
		}

		if existing, ok := rs.visited[mv.Path]; ok {
			if existing.Version != mv.Version {
				return fmt.Errorf("pkg: version conflict for %s: %s requires %s, already resolved at %s (minimal version selection is not yet implemented; add a replace directive to force one version)",
					mv.Path, m.Path, versionLabel(mv), existing.Version)
			}
			continue
		}
		if rs.visiting[mv.Path] {
			return fmt.Errorf("pkg: dependency cycle detected at %s (required by %s)", mv.Path, m.Path)
		}

		dm, err := rs.resolveModule(mv)
		if err != nil {
			return fmt.Errorf("pkg: resolving %s (required by %s): %w", mv.Path, m.Path, err)
		}

		rs.visited[mv.Path] = dm
		rs.visiting[mv.Path] = true
		if err := rs.walk(dm); err != nil {
			return err
		}
		rs.visiting[mv.Path] = false
	}

	rs.order = append(rs.order, m)
	return nil
}

// resolveModule loads mv onto disk. A filesystem-path replace target
// (mv.Version == "") is read directly, bypassing the cache and vs.sum
// entirely — it's local, actively-edited source, the same reason Go's
// `replace ... => ./dir` never touches go.sum. Everything else goes
// through rs.cache.Mod.
func (rs *resolveState) resolveModule(mv mod.ModuleVersion) (*Module, error) {
	var dir string
	if mv.Version == "" {
		dir = string(mv.Path)
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(rs.rootDir, dir)
		}
	} else {
		d, err := rs.cache.Mod(mv.Path, mv.Version, rs.sumPath, rs.mode)
		if err != nil {
			return nil, err
		}
		dir = d
	}

	modPath := filepath.Join(dir, "vs.mod")
	data, err := os.ReadFile(modPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	mf, err := mod.Parse(modPath, data, nil)
	if err != nil {
		return nil, err
	}
	lf, err := loadLibFile(dir)
	if err != nil {
		return nil, err
	}
	return &Module{Path: mv.Path, Version: mv.Version, Dir: dir, ModFile: mf, LibFile: lf}, nil
}

func versionLabel(mv mod.ModuleVersion) string {
	if mv.Version == "" {
		return fmt.Sprintf("local replace %s", mv.Path)
	}
	return mv.Version
}

func indexReplace(rs []*mod.Replace) map[mod.ModulePath]mod.ModuleVersion {
	m := make(map[mod.ModulePath]mod.ModuleVersion, len(rs))
	for _, r := range rs {
		m[r.Old.Path] = r.New
	}
	return m
}

func indexExclude(xs []*mod.Exclude) map[mod.ModuleVersion]bool {
	m := make(map[mod.ModuleVersion]bool, len(xs))
	for _, x := range xs {
		m[x.Mod] = true
	}
	return m
}

// loadLibFile parses dir's vs.lib if present, or returns (nil, nil) if
// the module has none. ParseLax, not Parse: an already-fetched
// dependency's vs.lib was validated by its own author's (possibly
// newer) toolchain, so an unrecognized field there shouldn't break
// every project that happens to depend on it.
func loadLibFile(dir string) (*lib.File, error) {
	path := filepath.Join(dir, "vs.lib")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("pkg: read %s: %w", path, err)
	}
	return lib.ParseLax(path, data)
}