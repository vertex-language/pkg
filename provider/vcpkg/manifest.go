package vcpkg

import "os"

// This file re-exports the Commit helper used by the top-level lock step so
// the environment layer can record the exact vcpkg registry snapshot alongside
// the installed package — enabling fully reproducible rebuilds.
//
// Usage from the environment layer (via the vcpkgAdapter):
//
//	commit, err := adapter.VcpkgCommit()
//	// store commit in index.lock as vcpkg_commit = "a34c873"

// VcpkgCommit returns the short HEAD commit of the vcpkg clone embedded in
// this environment. It returns an empty string (not an error) if the
// environment has never run a vcpkg install, since the clone may not exist.
func (v *Vcpkg) VcpkgCommit() (string, error) {
	if _, err := os.Stat(v.binaryPath()); err != nil {
		return "", nil // not bootstrapped yet, nothing to record
	}
	return v.Commit()
}