package apt

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// download fetches a .deb from the mirror into the cache dir and returns its local path.
// It fires Downloading, DownloadProgress (repeatedly), and DownloadDone on the logger.
func (a *Apt) download(img image, meta packageMeta) (string, error) {
	url := packageURL(img, meta.Filename)

	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}

	total := resp.ContentLength // -1 when unknown

	if a.logger != nil {
		a.logger.Downloading(meta.Name, meta.Version, total)
	}

	if err := os.MkdirAll(a.cacheDir, 0755); err != nil {
		return "", fmt.Errorf("download: mkdir: %w", err)
	}

	debName := filepath.Base(meta.Filename)
	destPath := filepath.Join(a.cacheDir, debName)

	f, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("download: create file: %w", err)
	}
	defer f.Close()

	var src io.Reader = resp.Body
	if a.logger != nil {
		src = &progressReader{
			r:       resp.Body,
			name:    meta.Name,
			total:   total,
			logger:  a.logger,
		}
	}

	if _, err := io.Copy(f, src); err != nil {
		return "", fmt.Errorf("download: write: %w", err)
	}

	if a.logger != nil {
		a.logger.DownloadDone(meta.Name, meta.Version)
	}

	return destPath, nil
}

// progressReader wraps an io.Reader and fires DownloadProgress on every read.
type progressReader struct {
	r        io.Reader
	name     string
	total    int64
	received int64
	lastPct  int // last percentage reported — throttles redundant calls
	logger   Logger
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	if n > 0 {
		p.received += int64(n)

		// Throttle to one call per percentage point (or always when size unknown).
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