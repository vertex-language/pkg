package winget

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// download fetches the installer to the cache directory, verifies its SHA-256
// hash, and returns the local path.
func (w *Winget) download(pkg, version string, ins Installer) (string, error) {
	resp, err := http.Get(ins.InstallerUrl)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", ins.InstallerUrl, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", ins.InstallerUrl, resp.StatusCode)
	}

	total := resp.ContentLength // -1 when unknown

	if w.logger != nil {
		w.logger.Downloading(pkg, version, total)
	}

	destPath := filepath.Join(w.cacheDir, cacheFileName(ins))

	f, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("download: create cache file: %w", err)
	}
	defer f.Close()

	hasher := sha256.New()

	var src io.Reader = resp.Body
	if w.logger != nil {
		src = &progressReader{
			r:      resp.Body,
			name:   pkg,
			total:  total,
			logger: w.logger,
		}
	}

	if _, err := io.Copy(io.MultiWriter(f, hasher), src); err != nil {
		os.Remove(destPath)
		return "", fmt.Errorf("download: write: %w", err)
	}

	if w.logger != nil {
		w.logger.DownloadDone(pkg, version)
	}

	// Verify hash when the manifest provides one.
	if ins.InstallerSha256 != "" {
		got := strings.ToUpper(hex.EncodeToString(hasher.Sum(nil)))
		want := strings.ToUpper(ins.InstallerSha256)
		if got != want {
			os.Remove(destPath)
			return "", fmt.Errorf("download %s: SHA-256 mismatch\n  got  %s\n  want %s", pkg, got, want)
		}
	}

	return destPath, nil
}

// cacheFileName derives a local file name for the installer.
// It uses the last path segment of the URL (minus query string) so that
// re-runs of the same version reuse the cached file.
func cacheFileName(ins Installer) string {
	u := ins.InstallerUrl
	if i := strings.LastIndexByte(u, '/'); i >= 0 {
		u = u[i+1:]
	}
	if i := strings.IndexByte(u, '?'); i >= 0 {
		u = u[:i]
	}
	if u == "" {
		return "installer" + installerExtension(ins.InstallerType)
	}
	return u
}

func installerExtension(t string) string {
	switch strings.ToLower(t) {
	case "msix", "msixbundle":
		return ".msix"
	case "appx", "appxbundle":
		return ".appx"
	case "msi":
		return ".msi"
	case "zip":
		return ".zip"
	case "portable":
		return ".exe"
	default:
		return ".exe"
	}
}

// progressReader wraps an io.Reader and fires DownloadProgress on every read,
// throttled to one call per percentage point.
type progressReader struct {
	r        io.Reader
	name     string
	total    int64
	received int64
	lastPct  int
	logger   Logger
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	if n > 0 {
		p.received += int64(n)
		pct := 0
		if p.total > 0 {
			pct = int(p.received * 100 / p.total)
		}
		if p.total <= 0 || pct != p.lastPct {
			p.logger.DownloadProgress(p.name, p.received, p.total)
			p.lastPct = pct
		}
	}
	return n, err
}