package brew

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// cellarLinkDirs are the keg subdirectories that get linked into the env root,
// matching the set that `brew link` populates.
var cellarLinkDirs = []string{
	"bin",
	"sbin",
	"lib",
	"libexec",
	"include",
	"share",
}

// versionedBinary matches binaries with a numeric suffix like gcc-15, g++-14, cpp-15.
// The captured group is the base name without the suffix.
var versionedBinary = regexp.MustCompile(`^(.+)-\d+(\.\d+)*$`)

// unpack extracts a Homebrew bottle tar.gz and links it into envDir.
//
// Layout produced:
//
//	envDir/
//	  Cellar/{pkg}/{kegVersion}/   ← full keg, extracted from the bottle
//	  bin/{binary} → ../Cellar/…  ← symlinks for each entry in linkDirs
//	  lib/…
//	  include/…
//	  …
//
// This mirrors the directory tree that a real Homebrew prefix uses so that
// consumers of the environment get the same paths they would see on a real
// macOS or Linux machine with Homebrew installed.
func unpack(bottlePath, pkg, kegVersion, envDir string) error {
	if err := extractBottle(bottlePath, pkg, kegVersion, envDir); err != nil {
		return err
	}
	kegDir := filepath.Join(envDir, "Cellar", pkg, kegVersion)
	return linkKeg(kegDir, envDir)
}

// extractBottle decompresses the bottle tar.gz and writes every entry under
// envDir/Cellar/{pkg}/{kegVersion}/, stripping the leading "{pkg}/{kegVersion}/"
// prefix that Homebrew embeds in all bottle archives.
func extractBottle(bottlePath, pkg, kegVersion, envDir string) error {
	f, err := os.Open(bottlePath)
	if err != nil {
		return fmt.Errorf("brew unpack: open bottle: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("brew unpack: gzip reader: %w", err)
	}
	defer gz.Close()

	tarPrefix := pkg + "/" + kegVersion + "/"
	kegRoot := filepath.Join(envDir, "Cellar", pkg, kegVersion)

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("brew unpack: read tar: %w", err)
		}

		name := filepath.ToSlash(strings.TrimPrefix(header.Name, "./"))
		if !strings.HasPrefix(name, tarPrefix) {
			continue
		}
		rel := strings.TrimPrefix(name, tarPrefix)
		if rel == "" || rel == "./" {
			continue
		}

		dest := filepath.Join(kegRoot, filepath.FromSlash(rel))

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, os.FileMode(header.Mode)|0755); err != nil {
				return fmt.Errorf("brew unpack: mkdir %s: %w", dest, err)
			}

		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				return fmt.Errorf("brew unpack: mkdir parent for %s: %w", dest, err)
			}
			if err := writeFile(dest, tr, header.Mode); err != nil {
				return fmt.Errorf("brew unpack: write %s: %w", rel, err)
			}

		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				return fmt.Errorf("brew unpack: mkdir parent for symlink %s: %w", dest, err)
			}
			os.Remove(dest)
			if err := os.Symlink(header.Linkname, dest); err != nil {
				return fmt.Errorf("brew unpack: symlink %s → %s: %w", dest, header.Linkname, err)
			}

		case tar.TypeLink:
			linkRel := filepath.FromSlash(strings.TrimPrefix(
				strings.TrimPrefix(filepath.ToSlash(header.Linkname), "./"),
				tarPrefix,
			))
			linkTarget := filepath.Join(kegRoot, linkRel)
			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				return fmt.Errorf("brew unpack: mkdir parent for hardlink %s: %w", dest, err)
			}
			os.Remove(dest)
			if err := os.Link(linkTarget, dest); err != nil {
				return fmt.Errorf("brew unpack: hardlink %s → %s: %w", dest, linkTarget, err)
			}
		}
	}
	return nil
}

// linkKeg creates symlinks in envDir/{bin,lib,…} pointing into the keg,
// mirroring what `brew link` does when activating a formula.
// Existing symlinks at the destination are replaced; regular files are left
// alone to avoid overwriting user files.
// For versioned binaries (e.g. gcc-15), an unversioned alias (gcc) is also
// created if one does not already exist — matching the behaviour of
// `brew link --overwrite` on a fresh prefix.
func linkKeg(kegDir, envDir string) error {
	for _, dir := range cellarLinkDirs {
		src := filepath.Join(kegDir, dir)
		if _, err := os.Stat(src); os.IsNotExist(err) {
			continue
		}

		dstDir := filepath.Join(envDir, dir)
		if err := os.MkdirAll(dstDir, 0755); err != nil {
			return fmt.Errorf("brew link: mkdir %s: %w", dstDir, err)
		}

		entries, err := os.ReadDir(src)
		if err != nil {
			return fmt.Errorf("brew link: readdir %s: %w", src, err)
		}

		for _, entry := range entries {
			srcFile := filepath.Join(src, entry.Name())
			dstFile := filepath.Join(dstDir, entry.Name())

			if err := placeSymlink(srcFile, dstFile); err != nil {
				return fmt.Errorf("brew link: symlink %s: %w", entry.Name(), err)
			}

			// If the binary has a versioned suffix (gcc-15 → gcc), create an
			// unversioned alias pointing to the same source unless something
			// already owns that name.
			if dir == "bin" || dir == "sbin" {
				if m := versionedBinary.FindStringSubmatch(entry.Name()); m != nil {
					aliasDst := filepath.Join(dstDir, m[1])
					if _, err := os.Lstat(aliasDst); os.IsNotExist(err) {
						_ = os.Symlink(srcFile, aliasDst)
					}
				}
			}
		}
	}
	return nil
}

// placeSymlink creates or replaces a symlink at dst pointing to src.
// Regular files and directories at dst are left untouched.
func placeSymlink(src, dst string) error {
	if fi, err := os.Lstat(dst); err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			return nil // leave regular files / directories intact
		}
		os.Remove(dst)
	}
	return os.Symlink(src, dst)
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