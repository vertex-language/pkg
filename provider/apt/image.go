package apt

import "fmt"

// image.go owns all Debian/Ubuntu mirror constants and URL patterns.
// Nothing outside this package ever sees these.

type image struct {
	Codename  string
	MirrorURL string
	Arch      string
}

var knownImages = map[string]image{
	"debian:11": {
		Codename:  "bullseye",
		MirrorURL: "http://deb.debian.org/debian",
		Arch:      "amd64",
	},
	"debian:12": {
		Codename:  "bookworm",
		MirrorURL: "http://deb.debian.org/debian",
		Arch:      "amd64",
	},
	"ubuntu:20.04": {
		Codename:  "focal",
		MirrorURL: "http://archive.ubuntu.com/ubuntu",
		Arch:      "amd64",
	},
	"ubuntu:22.04": {
		Codename:  "jammy",
		MirrorURL: "http://archive.ubuntu.com/ubuntu",
		Arch:      "amd64",
	},
	"ubuntu:24.04": {
		Codename:  "noble",
		MirrorURL: "http://archive.ubuntu.com/ubuntu",
		Arch:      "amd64",
	},
}

func resolveImage(platform string) (image, error) {
	img, ok := knownImages[platform]
	if !ok {
		return image{}, fmt.Errorf("unknown platform %q — supported: debian:11, debian:12, ubuntu:20.04, ubuntu:22.04, ubuntu:24.04", platform)
	}
	return img, nil
}

// packageIndexURL returns the gzipped Packages index URL for an image.
func packageIndexURL(img image) string {
	return fmt.Sprintf(
		"%s/dists/%s/main/binary-%s/Packages.gz",
		img.MirrorURL, img.Codename, img.Arch,
	)
}

// packageURL builds the full .deb download URL from a relative pool path.
func packageURL(img image, relativePath string) string {
	return img.MirrorURL + "/" + relativePath
}