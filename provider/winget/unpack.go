package winget

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// unpack places the installer contents into envDir based on the installer type.
//
// Type handling:
//
//	zip            — archive/zip extracts .exe/.dll entries into bin/
//	portable       — the file itself is the binary; placed directly in bin/
//	msix/appx      — MSIX/APPX are ZIP files; top-level .exe entries go to bin/
//	msi/exe/others — opaque Windows installers; copied to bin/ as-is so the
//	                 environment still contains the artifact for later use on Windows
func unpack(localPath string, ins Installer, envDir string) error {
	binDir := filepath.Join(envDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return fmt.Errorf("mkdir bin: %w", err)
	}

	switch strings.ToLower(ins.InstallerType) {
	case "zip":
		return unpackZip(localPath, ins, binDir)
	case "portable":
		return unpackPortable(localPath, ins, binDir)
	case "msix", "msixbundle", "appx", "appxbundle":
		return unpackMsix(localPath, binDir)
	default:
		// msi, exe, inno, nullsoft, wix, burn: copy the installer to bin/ as-is.
		// These require the Windows installer subsystem to actually run; we still
		// land the file in the environment so DownloadOnly-style workflows work.
		return copyToBin(localPath, binDir)
	}
}

// unpackZip extracts extractable files (.exe, .dll, .so, .dylib) from a zip.
// If the manifest specifies NestedInstallerFiles, only those paths are extracted.
func unpackZip(zipPath string, ins Installer, binDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	// Build a set of relative paths we want, if the manifest specified them.
	wantPaths := make(map[string]string) // normalised path → PortableCommandAlias
	for _, nf := range ins.NestedInstallerFiles {
		norm := filepath.ToSlash(nf.RelativeFilePath)
		wantPaths[norm] = nf.PortableCommandAlias
	}

	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		norm := filepath.ToSlash(f.Name)

		var destName string
		if len(wantPaths) > 0 {
			alias, ok := wantPaths[norm]
			if !ok {
				continue
			}
			if alias != "" {
				destName = alias
			} else {
				destName = filepath.Base(f.Name)
			}
		} else {
			base := filepath.Base(f.Name)
			if !isExtractable(base) {
				continue
			}
			destName = base
		}

		if err := extractZipEntry(f, filepath.Join(binDir, destName)); err != nil {
			return fmt.Errorf("extract %s: %w", f.Name, err)
		}
	}
	return nil
}

// unpackPortable copies the downloaded file into binDir.
// The target name is the PortableCommandAlias when provided, otherwise the
// URL base name.
func unpackPortable(src string, ins Installer, binDir string) error {
	name := filepath.Base(src)
	if len(ins.NestedInstallerFiles) > 0 {
		nf := ins.NestedInstallerFiles[0]
		if nf.PortableCommandAlias != "" {
			name = nf.PortableCommandAlias
			if !strings.Contains(name, ".") {
				name += ".exe"
			}
		}
	}
	return copyFile(src, filepath.Join(binDir, name), 0755)
}

// unpackMsix opens the MSIX/APPX (which is a ZIP) and extracts .exe files
// that sit at the top level of the archive — those are the application binaries.
// Localisation resources, DLLs, and the AppxManifest are skipped.
func unpackMsix(msixPath, binDir string) error {
	r, err := zip.OpenReader(msixPath)
	if err != nil {
		return fmt.Errorf("open msix: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		// Only take .exe files at the archive root (no sub-directory separator).
		name := f.Name
		if strings.ContainsRune(name, '/') {
			continue
		}
		if !strings.EqualFold(filepath.Ext(name), ".exe") {
			continue
		}
		if err := extractZipEntry(f, filepath.Join(binDir, name)); err != nil {
			return fmt.Errorf("extract %s from msix: %w", name, err)
		}
	}
	return nil
}

// copyToBin is the fallback for installer types we cannot decompose
// (msi, exe, inno, wix, etc.). The file is placed in bin/ under its original name.
func copyToBin(src, binDir string) error {
	return copyFile(src, filepath.Join(binDir, filepath.Base(src)), 0755)
}

// isExtractable returns true for binary file extensions we want to pull out
// of a generic zip when no explicit NestedInstallerFiles list is given.
func isExtractable(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".exe", ".dll", ".so", ".dylib":
		return true
	}
	return false
}

// ── helpers ───────────────────────────────────────────────────────────────────

func extractZipEntry(f *zip.File, destPath string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	return writeFile(destPath, rc, int64(f.Mode()))
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return writeFile(dst, in, int64(mode))
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