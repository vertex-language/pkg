package brew

import (
	"fmt"
	"runtime"
)

// preferredBottleTag returns the best bottle tag for the given OS/arch,
// or for a cross-target platform string when non-empty.
// On macOS the newest tag is returned; resolveBottle in brew.go falls back
// through macOSTagFallbackOrder when that tag is absent from a formula.
func preferredBottleTag(os, arch, platform string) (string, error) {
	if platform != "" {
		return tagForPlatform(platform)
	}
	return tagForHost(os, arch)
}

func tagForHost(os, arch string) (string, error) {
	switch os {
	case "linux":
		if arch == "arm64" {
			return "arm64_linux", nil
		}
		return "x86_64_linux", nil
	case "darwin":
		// Return the newest known tag; caller will fall back if absent.
		if arch == "arm64" {
			return "arm64_tahoe", nil
		}
		return "tahoe", nil
	default:
		return "", fmt.Errorf("brew: unsupported host OS %q", os)
	}
}

func tagForPlatform(platform string) (string, error) {
	switch platform {
	case "macos", "macos:arm64":
		return "arm64_tahoe", nil
	case "macos:amd64":
		return "tahoe", nil
	case "linux", "linux:amd64":
		return "x86_64_linux", nil
	case "linux:arm64":
		return "arm64_linux", nil
	default:
		return "", fmt.Errorf("brew: unknown platform %q", platform)
	}
}

// macOSTagFallbackOrder returns bottle tags in newest-first order for macOS.
// Used when the preferred tag is absent from a formula's bottle set.
func macOSTagFallbackOrder(arch string) []string {
	if arch == "arm64" {
		return []string{
			"arm64_tahoe",
			"arm64_sequoia",
			"arm64_sonoma",
			"arm64_ventura",
			"arm64_monterey",
			"arm64_big_sur",
		}
	}
	return []string{
		"tahoe",
		"sequoia",
		"sonoma",
		"ventura",
		"monterey",
		"big_sur",
	}
}

// hostArch returns the runtime arch in brew's naming convention.
func hostArch() string {
	if runtime.GOARCH == "arm64" {
		return "arm64"
	}
	return "amd64"
}