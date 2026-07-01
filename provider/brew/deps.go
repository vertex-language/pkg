package brew

import "fmt"

// resolveDeps performs a breadth-first walk of the formula dependency graph
// and returns the ordered list of dependency formulae (excluding the root).
// Each dependency is fetched individually from the Homebrew JSON API so we
// get its own transitives. Essential system formulae (e.g. glibc on Linux)
// that are absent from the API are warned and skipped rather than erroring.
func resolveDeps(root formulaMeta, logger Logger) ([]formulaMeta, error) {
	visited := map[string]bool{root.Name: true}
	var ordered []formulaMeta
	queue := append([]string(nil), root.Deps...)

	warn := func(format string, args ...any) {
		if logger != nil {
			logger.Warn(fmt.Sprintf(format, args...))
		}
	}

	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]

		if visited[name] {
			continue
		}
		visited[name] = true

		meta, err := fetchFormula(name)
		if err != nil {
			warn("dep %q: %v — skipping", name, err)
			continue
		}

		ordered = append(ordered, meta)
		queue = append(queue, meta.Deps...)
	}

	return ordered, nil
}