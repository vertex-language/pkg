package apt

import (
	"fmt"
	"strings"
)

// InstallPlan is the ordered list of packages to download+unpack.
type InstallPlan struct {
	PreDeps []packageMeta
	Deps    []packageMeta
}

// resolveDeps does a breadth-first walk of the full transitive dependency
// graph and returns an ordered InstallPlan.
func resolveDeps(root packageMeta, index map[string]packageMeta, logger Logger) (InstallPlan, error) {
	visited := make(map[string]bool)
	visited[root.Name] = true

	var preDeps []packageMeta
	var deps []packageMeta

	type work struct {
		meta  packageMeta
		isPre bool
	}
	var queue []work

	warn := func(format string, args ...any) {
		if logger != nil {
			logger.Warn(fmt.Sprintf(format, args...))
		}
	}

	for _, group := range parseDependsField(root.PreDepends) {
		m, err := resolveAlternatives(group, index)
		if err != nil {
			warn("pre-dep %q: %v", group, err)
			continue
		}
		if !visited[m.Name] {
			visited[m.Name] = true
			queue = append(queue, work{m, true})
		}
	}
	for _, group := range parseDependsField(root.Depends) {
		m, err := resolveAlternatives(group, index)
		if err != nil {
			warn("dep %q: %v", group, err)
			continue
		}
		if !visited[m.Name] {
			visited[m.Name] = true
			queue = append(queue, work{m, false})
		}
	}

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		pkg := item.meta

		if pkg.Essential {
			continue
		}

		if item.isPre {
			preDeps = append(preDeps, pkg)
		} else {
			deps = append(deps, pkg)
		}

		for _, group := range parseDependsField(pkg.PreDepends) {
			m, err := resolveAlternatives(group, index)
			if err != nil {
				warn("pre-dep %q of %q: %v", group, pkg.Name, err)
				continue
			}
			if !visited[m.Name] {
				visited[m.Name] = true
				queue = append(queue, work{m, true})
			}
		}

		for _, group := range parseDependsField(pkg.Depends) {
			m, err := resolveAlternatives(group, index)
			if err != nil {
				warn("dep %q of %q: %v", group, pkg.Name, err)
				continue
			}
			if !visited[m.Name] {
				visited[m.Name] = true
				queue = append(queue, work{m, item.isPre})
			}
		}
	}

	return InstallPlan{PreDeps: preDeps, Deps: deps}, nil
}

func parseDependsField(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	for _, clause := range strings.Split(raw, ",") {
		clause = strings.TrimSpace(clause)
		if clause != "" {
			out = append(out, clause)
		}
	}
	return out
}

func resolveAlternatives(group string, index map[string]packageMeta) (packageMeta, error) {
	alts := strings.Split(group, "|")
	var lastErr error
	for _, alt := range alts {
		name, op, version := parseDepAtom(strings.TrimSpace(alt))
		meta, ok := index[name]
		if !ok {
			lastErr = fmt.Errorf("%q not in index", name)
			continue
		}
		if version != "" && !satisfiesVersion(meta.Version, op, version) {
			lastErr = fmt.Errorf("%q: version %q does not satisfy %s %q",
				name, meta.Version, op, version)
			continue
		}
		return meta, nil
	}
	return packageMeta{}, fmt.Errorf("no alternative satisfied for %q: %w", group, lastErr)
}

func parseDepAtom(atom string) (name, op, version string) {
	if i := strings.IndexByte(atom, '['); i >= 0 {
		atom = strings.TrimSpace(atom[:i])
	}
	if i := strings.IndexByte(atom, '('); i >= 0 {
		vc := atom[i+1:]
		atom = strings.TrimSpace(atom[:i])
		if j := strings.IndexByte(vc, ')'); j >= 0 {
			vc = strings.TrimSpace(vc[:j])
		}
		for _, o := range []string{">=", "<=", ">>", "<<", "="} {
			if strings.HasPrefix(vc, o) {
				op = o
				version = strings.TrimSpace(strings.TrimPrefix(vc, o))
				break
			}
		}
	}
	return strings.TrimSpace(atom), op, version
}

func satisfiesVersion(available, op, required string) bool {
	if required == "" {
		return true
	}
	return satisfiesConstraint(available, op, required)
}