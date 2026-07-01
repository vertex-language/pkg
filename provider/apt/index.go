package apt

import (
    "bufio"
    "compress/gzip"
    "fmt"
    "io"
    "net/http"
    "strings"
)

type packageMeta struct {
    Name       string
    Version    string
    Filename   string
    SHA256     string
    Depends    string // "libc6 (>= 2.36), binutils (>= 2.40), cpp-12 | cpp"
    PreDepends string // must be installed+configured before unpack
    Provides   string // virtual package names this package satisfies
    Essential  bool   // if true, assumed present on any Debian system
}

func fetchPackageIndex(img image) (map[string]packageMeta, error) {
    url := packageIndexURL(img)
    resp, err := http.Get(url)
    if err != nil {
        return nil, fmt.Errorf("fetch index %s: %w", url, err)
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("fetch index %s: status %d", url, resp.StatusCode)
    }
    gz, err := gzip.NewReader(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("decompress index: %w", err)
    }
    defer gz.Close()
    return parsePackageIndex(gz)
}

func parsePackageIndex(r io.Reader) (map[string]packageMeta, error) {
    // name-keyed primary index
    packages := make(map[string]packageMeta)
    scanner := bufio.NewScanner(r)
    scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

    var cur packageMeta

    flush := func() {
        if cur.Name == "" {
            return
        }
        packages[cur.Name] = cur

        // register each virtual name from Provides so lookups work transparently
        if cur.Provides != "" {
            for _, raw := range strings.Split(cur.Provides, ",") {
                vname := parseVirtualName(strings.TrimSpace(raw))
                if vname != "" && vname != cur.Name {
                    // only register if no real package has that name yet
                    if _, exists := packages[vname]; !exists {
                        packages[vname] = cur
                    }
                }
            }
        }
        cur = packageMeta{}
    }

    for scanner.Scan() {
        line := scanner.Text()
        if line == "" {
            flush()
            continue
        }
        key, value, ok := strings.Cut(line, ": ")
        if !ok {
            continue
        }
        switch key {
        case "Package":
            cur.Name = value
        case "Version":
            cur.Version = value
        case "Filename":
            cur.Filename = value
        case "SHA256":
            cur.SHA256 = value
        case "Depends":
            cur.Depends = value
        case "Pre-Depends":
            cur.PreDepends = value
        case "Provides":
            cur.Provides = value
        case "Essential":
            cur.Essential = strings.ToLower(strings.TrimSpace(value)) == "yes"
        }
    }
    flush()

    if err := scanner.Err(); err != nil {
        return nil, fmt.Errorf("parse index: %w", err)
    }
    return packages, nil
}

// parseVirtualName strips a version constraint from a Provides entry.
// "libc6-udeb (= 2.36)" → "libc6-udeb"
func parseVirtualName(s string) string {
    if i := strings.IndexByte(s, '('); i >= 0 {
        s = strings.TrimSpace(s[:i])
    }
    return s
}

func findPackage(index map[string]packageMeta, pkg, version string) (packageMeta, error) {
    meta, ok := index[pkg]
    if !ok {
        return packageMeta{}, fmt.Errorf("package %q not found in index", pkg)
    }
    if version != "" && !strings.HasPrefix(meta.Version, version) {
        return packageMeta{}, fmt.Errorf(
            "package %q: requested %q, available %q", pkg, version, meta.Version)
    }
    return meta, nil
}