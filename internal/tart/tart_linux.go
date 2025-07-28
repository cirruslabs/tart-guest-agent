package tart

import (
	"os"
	"path/filepath"
	"strings"
)

const virtioPortsDirectoryPath = "/dev/virtio-ports"

func LocateCommunicationPoint() (string, bool) {
	dirEntries, err := os.ReadDir(virtioPortsDirectoryPath)
	if err != nil {
		return "", false
	}

	for _, entry := range dirEntries {
		if strings.HasPrefix(entry.Name(), "tart-version-") {
			return filepath.Join(virtioPortsDirectoryPath, entry.Name()), true
		}
	}

	return "", false
}
