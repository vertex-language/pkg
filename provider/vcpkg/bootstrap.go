package vcpkg

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

const vcpkgRepo = "https://github.com/microsoft/vcpkg.git"

// ensureBootstrapped guarantees the vcpkg binary exists inside the environment.
// Idempotent: returns immediately if the binary is already present.
func (v *Vcpkg) ensureBootstrapped() error {
	if err := v.ensureDirs(); err != nil {
		return err
	}
	if _, err := os.Stat(v.binaryPath()); err == nil {
		return nil // already bootstrapped
	}
	if _, err := os.Stat(v.vcpkgDir); os.IsNotExist(err) {
		if err := v.clone(); err != nil {
			return err
		}
	}
	return v.runBootstrapScript()
}

// clone performs a shallow clone of the vcpkg repository using go-git.
func (v *Vcpkg) clone() error {
	_, err := git.PlainClone(v.vcpkgDir, false, &git.CloneOptions{
		URL:          vcpkgRepo,
		Depth:        1,
		SingleBranch: true,
		ReferenceName: plumbing.NewBranchReferenceName("master"),
	})
	if err != nil {
		return fmt.Errorf("vcpkg: clone: %w", err)
	}
	return nil
}

// runBootstrapScript executes bootstrap-vcpkg.sh / bootstrap-vcpkg.bat to
// produce the vcpkg binary. This is the only exec call in the file because the
// bootstrap script itself is a shell/batch script — there is no Go equivalent.
func (v *Vcpkg) runBootstrapScript() error {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		bat := filepath.Join(v.vcpkgDir, "bootstrap-vcpkg.bat")
		cmd = exec.Command("cmd.exe", "/C", bat, "-disableMetrics")
	} else {
		sh := filepath.Join(v.vcpkgDir, "bootstrap-vcpkg.sh")
		if err := os.Chmod(sh, 0755); err != nil {
			return fmt.Errorf("vcpkg: chmod bootstrap script: %w", err)
		}
		cmd = exec.Command(sh, "-disableMetrics")
	}
	cmd.Dir = v.vcpkgDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("vcpkg: bootstrap script: %w", err)
	}
	return nil
}

// Commit returns the short HEAD hash of the vcpkg clone via go-git —
// used by the lock step to stamp vcpkg_commit into index.lock.
func (v *Vcpkg) Commit() (string, error) {
	repo, err := git.PlainOpen(v.vcpkgDir)
	if err != nil {
		return "", fmt.Errorf("vcpkg: open repo: %w", err)
	}
	ref, err := repo.Head()
	if err != nil {
		return "", fmt.Errorf("vcpkg: read HEAD: %w", err)
	}
	return ref.Hash().String()[:7], nil
}