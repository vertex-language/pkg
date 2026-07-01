package winget

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// githubContentsAPI is used to list version directories.
	githubContentsAPI = "https://api.github.com/repos/microsoft/winget-pkgs/contents/manifests"
	// githubRaw is used to fetch raw YAML files.
	githubRaw = "https://raw.githubusercontent.com/microsoft/winget-pkgs/master/manifests"
)

// InstallerManifest is the parsed .installer.yaml for a specific package version.
type InstallerManifest struct {
	PackageIdentifier string      `yaml:"PackageIdentifier"`
	PackageVersion    string      `yaml:"PackageVersion"`
	// InstallerType at the top level is the default for all installers that
	// don't override it individually.
	InstallerType string      `yaml:"InstallerType"`
	Installers    []Installer `yaml:"Installers"`
}

// Installer is one entry in the Installers list.
type Installer struct {
	Architecture         string            `yaml:"Architecture"`
	InstallerType        string            `yaml:"InstallerType"`
	InstallerUrl         string            `yaml:"InstallerUrl"`
	InstallerSha256      string            `yaml:"InstallerSha256"`
	NestedInstallerType  string            `yaml:"NestedInstallerType"`
	NestedInstallerFiles []NestedInstaller `yaml:"NestedInstallerFiles"`
}

// NestedInstaller describes an executable inside a zip archive.
type NestedInstaller struct {
	RelativeFilePath     string `yaml:"RelativeFilePath"`
	PortableCommandAlias string `yaml:"PortableCommandAlias"`
}

// githubItem is one entry from the GitHub Contents API response.
type githubItem struct {
	Name string `json:"name"`
	Type string `json:"type"` // "dir" | "file"
}

// fetchInstallerManifest resolves the best version for pkg, then fetches and
// parses its .installer.yaml from the winget-pkgs GitHub repository.
//
// pkg must be a winget PackageIdentifier e.g. "Microsoft.PowerShell".
// version may be empty (latest) or a version prefix e.g. "7" or "7.6".
func fetchInstallerManifest(pkg, version string) (*InstallerManifest, error) {
	publisher, packageName, err := splitIdentifier(pkg)
	if err != nil {
		return nil, err
	}
	letter := strings.ToLower(string([]rune(publisher)[0]))

	// 1. List available version directories from GitHub.
	contentsURL := fmt.Sprintf("%s/%s/%s/%s",
		githubContentsAPI, letter, publisher, packageName)

	versions, err := listGitHubDir(contentsURL)
	if err != nil {
		return nil, fmt.Errorf("list versions for %q: %w", pkg, err)
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("no versions found for %q", pkg)
	}

	// 2. Pick the best matching version.
	resolved, err := resolveVersion(versions, version)
	if err != nil {
		return nil, err
	}

	// 3. Fetch raw installer YAML.
	rawURL := fmt.Sprintf("%s/%s/%s/%s/%s/%s.installer.yaml",
		githubRaw, letter, publisher, packageName, resolved, pkg)

	manifest, err := fetchInstallerYAML(rawURL)
	if err != nil {
		return nil, fmt.Errorf("fetch installer manifest for %s@%s: %w", pkg, resolved, err)
	}

	return manifest, nil
}

// splitIdentifier splits "Publisher.PackageName" on the first dot.
// "Microsoft.PowerShell"          → "Microsoft", "PowerShell"
// "Microsoft.VisualStudio.2022.CE" → "Microsoft", "VisualStudio.2022.CE"
func splitIdentifier(pkg string) (publisher, name string, err error) {
	i := strings.IndexByte(pkg, '.')
	if i < 0 {
		return "", "", fmt.Errorf("invalid package identifier %q: expected Publisher.Package format", pkg)
	}
	return pkg[:i], pkg[i+1:], nil
}

// listGitHubDir calls the GitHub Contents API and returns all directory entry
// names (version folders).
func listGitHubDir(url string) ([]string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "carbon-os/environment")
	// Honour an optional GitHub token to raise the rate limit.
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotFound:
		return nil, fmt.Errorf("package not found in winget-pkgs (check the identifier): %s", url)
	case http.StatusForbidden, http.StatusTooManyRequests:
		return nil, fmt.Errorf("github rate limit hit — set GITHUB_TOKEN to raise the limit")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github GET %s: unexpected status %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var items []githubItem
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("parse github contents: %w", err)
	}

	var dirs []string
	for _, it := range items {
		if it.Type == "dir" {
			dirs = append(dirs, it.Name)
		}
	}
	return dirs, nil
}

// resolveVersion picks the best available version for the given request.
//   - empty request  → the highest available version
//   - prefix request → the highest version whose string starts with the prefix
func resolveVersion(available []string, requested string) (string, error) {
	sorted := make([]string, len(available))
	copy(sorted, available)
	sort.Slice(sorted, func(i, j int) bool {
		return compareWingetVersions(sorted[i], sorted[j]) < 0
	})

	if requested == "" {
		return sorted[len(sorted)-1], nil
	}

	var matches []string
	for _, v := range sorted {
		if strings.HasPrefix(v, requested) {
			matches = append(matches, v)
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("version %q not found; available: %s",
			requested, strings.Join(sorted, ", "))
	}
	return matches[len(matches)-1], nil
}

// fetchInstallerYAML fetches and parses the raw installer YAML at url.
// It also propagates any top-level InstallerType default down into installers
// that don't declare their own type.
func fetchInstallerYAML(url string) (*InstallerManifest, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("installer manifest not found at %s", url)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: status %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var m InstallerManifest
	if err := yaml.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("parse installer YAML: %w", err)
	}

	// Propagate top-level default InstallerType to entries that omit their own.
	for i := range m.Installers {
		if m.Installers[i].InstallerType == "" {
			m.Installers[i].InstallerType = m.InstallerType
		}
	}

	return &m, nil
}

// selectInstaller picks the best Installer entry for the requested arch.
// Priority: exact arch match > "neutral" > first listed.
func selectInstaller(m *InstallerManifest, arch string) (Installer, error) {
	var neutral *Installer
	for i := range m.Installers {
		ins := &m.Installers[i]
		if strings.EqualFold(ins.Architecture, arch) {
			return *ins, nil
		}
		if strings.EqualFold(ins.Architecture, "neutral") {
			neutral = ins
		}
	}
	if neutral != nil {
		return *neutral, nil
	}
	if len(m.Installers) > 0 {
		return m.Installers[0], nil
	}
	return Installer{}, fmt.Errorf("no installer for arch %q in %s@%s",
		arch, m.PackageIdentifier, m.PackageVersion)
}

// ── version comparison ────────────────────────────────────────────────────────

// compareWingetVersions does a segment-by-segment numeric comparison.
// "2.0.10" > "2.0.9", "7.6.0.0" > "7.5.9.9".
func compareWingetVersions(a, b string) int {
	pa := strings.Split(a, ".")
	pb := strings.Split(b, ".")
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var sa, sb string
		if i < len(pa) {
			sa = pa[i]
		}
		if i < len(pb) {
			sb = pb[i]
		}
		if c := compareNumericSegment(sa, sb); c != 0 {
			return c
		}
	}
	return 0
}

func compareNumericSegment(a, b string) int {
	// Trim leading zeros then compare by length (longer = bigger) then
	// lexicographically (safe because same length).
	a = strings.TrimLeft(a, "0")
	b = strings.TrimLeft(b, "0")
	// Strip any non-digit suffix (e.g. "1-beta") before length comparison.
	a = numericPrefix(a)
	b = numericPrefix(b)
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func numericPrefix(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}