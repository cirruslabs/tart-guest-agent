package tart

import (
	"os"
	"path/filepath"
	"strings"
)

const devDirectoryPath = "/dev"

func LocateCommunicationPoint() (string, bool) {
	dirEntries, err := os.ReadDir(devDirectoryPath)
	if err != nil {
		return "", false
	}

	for _, entry := range dirEntries {
		if strings.HasPrefix(entry.Name(), "cu.tart-version-") {
			return filepath.Join(devDirectoryPath, entry.Name()), true
		}
	}

	return "", false
}
