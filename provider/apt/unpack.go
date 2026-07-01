package apt

import (
	"archive/tar"
	"compress/bzip2"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/blakesmith/ar"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

// extractPrefixes lists every path inside a .deb data.tar that we keep.
// Entries are matched against the normalized tar path (no leading "./").
var extractPrefixes = []string{
	"usr/bin/",
	"usr/lib/",
	"usr/libexec/",
	"usr/include/",
	"lib/",
}

// unpack extracts relevant paths from a .deb into envDir, preserving the
// full directory structure (usr/lib/…, usr/include/…, etc.).
//
// .deb structure (ar archive):
//
//	debian-binary   — format version
//	control.tar.*   — package metadata (skipped)
//	data.tar.*      — actual files
func unpack(debPath, envDir string) error {
	f, err := os.Open(debPath)
	if err != nil {
		return fmt.Errorf("unpack: open: %w", err)
	}
	defer f.Close()

	reader := ar.NewReader(f)

	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("unpack: read ar: %w", err)
		}

		if !strings.HasPrefix(header.Name, "data.tar") {
			continue
		}

		return extractDataTar(reader, header.Name, envDir)
	}

	return fmt.Errorf("unpack: data.tar not found in %s", debPath)
}

// extractDataTar decompresses and extracts matching paths from data.tar.*.
// The full relative path is preserved under envDir so that, for example,
// "usr/lib/x86_64-linux-gnu/libc.so.6" lands at envDir/usr/lib/…/libc.so.6.
func extractDataTar(r io.Reader, name, envDir string) error {
	tr, err := decompressedTar(r, name)
	if err != nil {
		return err
	}

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("extract: read tar: %w", err)
		}

		normalized := strings.TrimPrefix(header.Name, "./")

		// check whether this entry falls under one of our kept prefixes
		var keep bool
		for _, pfx := range extractPrefixes {
			if strings.HasPrefix(normalized, pfx) {
				keep = true
				break
			}
		}
		if !keep {
			continue
		}

		destPath := filepath.Join(envDir, normalized)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(destPath, os.FileMode(header.Mode)|0755); err != nil {
				return fmt.Errorf("extract: mkdir %s: %w", destPath, err)
			}

		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				return fmt.Errorf("extract: mkdir parent for %s: %w", destPath, err)
			}
			if err := writeFile(destPath, tr, header.Mode); err != nil {
				return fmt.Errorf("extract: write %s: %w", destPath, err)
			}

		case tar.TypeSymlink:
			// e.g. libgcc_s.so → libgcc_s.so.1
			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				return fmt.Errorf("extract: mkdir parent for symlink %s: %w", destPath, err)
			}
			os.Remove(destPath) // safe to ignore — may not exist yet
			if err := os.Symlink(header.Linkname, destPath); err != nil {
				return fmt.Errorf("extract: symlink %s → %s: %w", destPath, header.Linkname, err)
			}

		case tar.TypeLink:
			// hard link — Linkname is relative to the archive root
			linkTarget := filepath.Join(envDir, strings.TrimPrefix(header.Linkname, "./"))
			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				return fmt.Errorf("extract: mkdir parent for hardlink %s: %w", destPath, err)
			}
			os.Remove(destPath)
			if err := os.Link(linkTarget, destPath); err != nil {
				return fmt.Errorf("extract: hardlink %s → %s: %w", destPath, linkTarget, err)
			}
		}
	}

	return nil
}

// decompressedTar wraps r in the correct decompressor based on the data.tar filename.
func decompressedTar(r io.Reader, name string) (*tar.Reader, error) {
	switch {
	case strings.HasSuffix(name, ".gz"):
		gz, err := gzip.NewReader(r)
		if err != nil {
			return nil, fmt.Errorf("decompress gz: %w", err)
		}
		return tar.NewReader(gz), nil

	case strings.HasSuffix(name, ".xz"):
		xzr, err := xz.NewReader(r)
		if err != nil {
			return nil, fmt.Errorf("decompress xz: %w", err)
		}
		return tar.NewReader(xzr), nil

	case strings.HasSuffix(name, ".bz2"):
		return tar.NewReader(bzip2.NewReader(r)), nil

	case strings.HasSuffix(name, ".zst"):
		zr, err := zstd.NewReader(r)
		if err != nil {
			return nil, fmt.Errorf("decompress zstd: %w", err)
		}
		return tar.NewReader(zr), nil

	default:
		return nil, fmt.Errorf("unknown data.tar compression: %s", name)
	}
}

func writeFile(path string, r io.Reader, mode int64) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(mode))
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}