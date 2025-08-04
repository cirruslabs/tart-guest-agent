package tart

import (
	"github.com/Masterminds/semver/v3"
	"os"
	"strings"
)

const devDirectoryPath = "/dev"

func Version() (*semver.Version, bool) {
	dirEntries, err := os.ReadDir(devDirectoryPath)
	if err != nil {
		return nil, false
	}

	for _, entry := range dirEntries {
		versionRaw, found := strings.CutPrefix(entry.Name(), "cu.tart-version-")
		if !found {
			continue
		}

		version, err := semver.NewVersion(versionRaw)
		if err != nil {
			continue
		}

		if version.Major() < 2 {
			continue
		}

		return version, true
	}

	return nil, false
}
