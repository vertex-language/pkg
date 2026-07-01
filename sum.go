package pkg

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vertex-language/pkg/parser/mod"
)

// readSum reads and parses a vs.sum file:
//
//	<module path> <version> h1:<base64 sha256 tree hash>
//
// A missing file is not an error — it parses as empty, the state of a
// brand new project before its first successful build.
func readSum(path string) (map[mod.ModuleVersion]string, error) {
	sums := make(map[mod.ModuleVersion]string)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return sums, nil
		}
		return nil, fmt.Errorf("pkg: read %s: %w", path, err)
	}

	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, fmt.Errorf("pkg: %s: malformed line %q", path, line)
		}
		mv := mod.ModuleVersion{Path: mod.ModulePath(fields[0]), Version: fields[1]}
		sums[mv] = fields[2]
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("pkg: read %s: %w", path, err)
	}
	return sums, nil
}

// writeSum writes sums back to path in sorted, deterministic order —
// module path, then version — so vs.sum diffs cleanly under version
// control the same way go.sum does.
func writeSum(path string, sums map[mod.ModuleVersion]string) error {
	keys := make([]mod.ModuleVersion, 0, len(sums))
	for k := range sums {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Path != keys[j].Path {
			return keys[i].Path < keys[j].Path
		}
		return keys[i].Version < keys[j].Version
	})

	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s %s %s\n", k.Path, k.Version, sums[k])
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("pkg: write %s: %w", path, err)
	}
	return nil
}

// hashDir computes a deterministic content hash of every regular file
// under dir, in the style of Go's dirhash.Hash1: each file is hashed
// individually, then the sorted list of "<hex sha256>  <slash path>\n"
// lines is itself hashed. Sorting file order and using a fixed line
// format is what makes the result independent of extraction order,
// file mode, and OS path separators — the same tree hashes identically
// on Linux, macOS, and Windows.
func hashDir(dir string) (string, error) {
	var names []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		names = append(names, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("pkg: walk %s: %w", dir, err)
	}
	sort.Strings(names)

	h := sha256.New()
	for _, name := range names {
		fh, err := hashFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%x  %s\n", fh, name)
	}
	return "h1:" + base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

func hashFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("pkg: hash %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, fmt.Errorf("pkg: hash %s: %w", path, err)
	}
	return h.Sum(nil), nil
}