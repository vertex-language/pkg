package brew

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// ghcrAnonymousToken is the hard-coded anonymous bearer credential for ghcr.io.
// Homebrew documents and uses this as the canonical public-access token;
// an explicit token can also be obtained from:
//
//	GET https://ghcr.io/token?scope=repository:homebrew/core/{pkg}:pull
const ghcrAnonymousToken = "QQ=="

// download fetches a Homebrew bottle blob from ghcr.io into the cache dir
// and returns the local path to the downloaded file.
func (b *Brew) download(meta formulaMeta, file bottleFile) (string, error) {
	req, err := http.NewRequest(http.MethodGet, file.URL, nil)
	if err != nil {
		return "", fmt.Errorf("brew download %s: build request: %w", meta.Name, err)
	}
	// ghcr.io requires a bearer token even for public images.
	req.Header.Set("Authorization", "Bearer "+ghcrAnonymousToken)
	// Follow redirects to pkg-containers.githubusercontent.com (the CDN).
	// http.Client follows redirects by default; Authorization is stripped on
	// cross-host redirect, which is correct here — the CDN needs no auth.

	resp, err := b.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("brew download %s: %w", meta.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("brew download %s: HTTP %d", meta.Name, resp.StatusCode)
	}

	total := resp.ContentLength // -1 when server omits Content-Length
	if b.logger != nil {
		b.logger.Downloading(meta.Name, meta.Version, total)
	}

	if err := os.MkdirAll(b.cacheDir, 0755); err != nil {
		return "", fmt.Errorf("brew download: mkdir cache: %w", err)
	}

	// Filename mirrors brew's own cache naming: {name}--{keg_version}.bottle.tar.gz
	filename := fmt.Sprintf("%s--%s.bottle.tar.gz", meta.Name, meta.kegVersion())
	destPath := filepath.Join(b.cacheDir, filename)

	f, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("brew download: create %s: %w", filename, err)
	}
	defer f.Close()

	var src io.Reader = resp.Body
	if b.logger != nil {
		src = &progressReader{
			r:      resp.Body,
			name:   meta.Name,
			total:  total,
			logger: b.logger,
		}
	}

	if _, err := io.Copy(f, src); err != nil {
		return "", fmt.Errorf("brew download: write %s: %w", filename, err)
	}

	if b.logger != nil {
		b.logger.DownloadDone(meta.Name, meta.Version)
	}

	return destPath, nil
}

// progressReader wraps an io.Reader and fires DownloadProgress on every read.
// Throttled to one call per percentage point when Content-Length is known.
type progressReader struct {
	r       io.Reader
	name    string
	total   int64
	recv    int64
	lastPct int
	logger  Logger
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	if n > 0 {
		p.recv += int64(n)
		pct := 0
		if p.total > 0 {
			pct = int(p.recv * 100 / p.total)
		}
		if p.total <= 0 || pct != p.lastPct {
			p.logger.DownloadProgress(p.name, p.recv, p.total)
			p.lastPct = pct
		}
	}
	return n, err
}