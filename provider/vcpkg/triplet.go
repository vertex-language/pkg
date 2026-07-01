package vcpkg

import (
	"fmt"
	"runtime"
	"strings"
	"unicode"
)

// ResolveTriplet returns the vcpkg triplet string for the current host arch
// and the given optional platform target (e.g. "debian:12", "macos", "").
func ResolveTriplet(platform string) (string, error) {
	arch, err := vcpkgArch(runtime.GOARCH)
	if err != nil {
		return "", err
	}

	targetOS := runtime.GOOS
	if platform != "" {
		targetOS = osFromPlatform(platform)
	}

	os, err := vcpkgOS(targetOS)
	if err != nil {
		return "", err
	}

	return arch + "-" + os, nil
}

func vcpkgArch(goarch string) (string, error) {
	switch goarch {
	case "amd64":
		return "x64", nil
	case "arm64":
		return "arm64", nil
	case "386":
		return "x86", nil
	default:
		return "", fmt.Errorf("unsupported arch for vcpkg: %q", goarch)
	}
}

func vcpkgOS(goos string) (string, error) {
	switch goos {
	case "linux":
		return "linux", nil
	case "darwin":
		return "osx", nil
	case "windows":
		return "windows", nil
	default:
		return "", fmt.Errorf("unsupported OS for vcpkg: %q", goos)
	}
}

func osFromPlatform(platform string) string {
	switch {
	case strings.HasPrefix(platform, "debian:"),
		strings.HasPrefix(platform, "ubuntu:"):
		return "linux"
	case platform == "macos":
		return "darwin"
	case strings.HasPrefix(platform, "windows"):
		return "windows"
	default:
		return runtime.GOOS
	}
}

func normaliseName(pkg string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(pkg) {
		if r == '_' {
			b.WriteRune('-')
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}