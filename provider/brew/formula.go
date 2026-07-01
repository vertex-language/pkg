package brew

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const formulaAPIBase = "https://formulae.brew.sh/api/formula"

// formulaMeta is the normalised subset of the Homebrew JSON API response
// that the provider needs.
type formulaMeta struct {
	Name     string
	Version  string // stable version, e.g. "1.25.0"
	Revision int    // formula revision; appended to the keg dir as _N when > 0
	Deps     []string
	Bottles  map[string]bottleFile // bottle tag → file info
}

// kegVersion returns the directory name used inside the bottle tar and Cellar.
// e.g. "1.25.0" when revision == 0, "1.25.0_1" when revision == 1.
func (m *formulaMeta) kegVersion() string {
	if m.Revision > 0 {
		return fmt.Sprintf("%s_%d", m.Version, m.Revision)
	}
	return m.Version
}

// bottleFile is the per-platform entry from bottle.stable.files.
type bottleFile struct {
	URL    string
	SHA256 string
	Cellar string // original cellar path, e.g. "/opt/homebrew/Cellar"
}

// formulaJSON is the JSON envelope returned by formulae.brew.sh/api/formula/{name}.json.
type formulaJSON struct {
	Name     string `json:"name"`
	Versions struct {
		Stable string `json:"stable"`
	} `json:"versions"`
	Revision     int      `json:"revision"`
	Dependencies []string `json:"dependencies"`
	Bottle       struct {
		Stable struct {
			Files map[string]struct {
				Cellar string `json:"cellar"`
				URL    string `json:"url"`
				SHA256 string `json:"sha256"`
			} `json:"files"`
		} `json:"stable"`
	} `json:"bottle"`
}

// fetchFormula fetches and parses the formula metadata from the Homebrew JSON API.
func fetchFormula(pkg string) (formulaMeta, error) {
	url := fmt.Sprintf("%s/%s.json", formulaAPIBase, pkg)

	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return formulaMeta{}, fmt.Errorf("fetch formula %s: %w", pkg, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return formulaMeta{}, fmt.Errorf("formula %q not found", pkg)
	default:
		return formulaMeta{}, fmt.Errorf("fetch formula %s: HTTP %d", pkg, resp.StatusCode)
	}

	var f formulaJSON
	if err := json.NewDecoder(resp.Body).Decode(&f); err != nil {
		return formulaMeta{}, fmt.Errorf("parse formula %s: %w", pkg, err)
	}

	bottles := make(map[string]bottleFile, len(f.Bottle.Stable.Files))
	for tag, entry := range f.Bottle.Stable.Files {
		bottles[tag] = bottleFile{
			URL:    entry.URL,
			SHA256: entry.SHA256,
			Cellar: entry.Cellar,
		}
	}

	return formulaMeta{
		Name:     f.Name,
		Version:  f.Versions.Stable,
		Revision: f.Revision,
		Deps:     f.Dependencies,
		Bottles:  bottles,
	}, nil
}

// findBottle returns the bottleFile for the given tag, or an error listing
// what tags are available.
func findBottle(meta formulaMeta, tag string) (bottleFile, error) {
	if f, ok := meta.Bottles[tag]; ok {
		return f, nil
	}
	tags := make([]string, 0, len(meta.Bottles))
	for t := range meta.Bottles {
		tags = append(tags, t)
	}
	return bottleFile{}, fmt.Errorf(
		"no bottle for tag %q in formula %q — available: %s",
		tag, meta.Name, strings.Join(tags, ", "),
	)
}