package diskresizer

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/samber/lo"
	"howett.net/plist"
	"os/exec"
	"runtime"
	"strings"
)

type DiskUtilOutput struct {
	AllDisksAndPartitions []DiskWithPartitions `plist:"AllDisksAndPartitions"`
}

type DiskWithPartitions struct {
	DeviceIdentifier string      `plist:"DeviceIdentifier"`
	Size             int64       `plist:"Size"`
	Partitions       []Partition `plist:"Partitions"`
}

type Partition struct {
	DeviceIdentifier string `plist:"DeviceIdentifier"`
	Size             int64  `plist:"Size"`
	Content          string `plist:"Content"`
}

const expectedPartitionContent = "Apple_APFS"

var (
	ErrUnsupported    = errors.New("disk resizing is only supported on macOS")
	ErrAlreadyResized = errors.New("disk already seems to be resized")
)

func Resize() error {
	// Only macOS is currently supported because Linux distributions
	// already have this functionality
	if runtime.GOOS != "darwin" {
		return ErrUnsupported
	}

	// Obtain a list of physical disks with their partitions
	cmd := exec.Command("diskutil", "list", "-plist", "physical")

	stderrBuf := &bytes.Buffer{}
	cmd.Stderr = stderrBuf

	diskutilOutput, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to list physical disks: %w: %s", err,
			firstNonEmptyLine(stderrBuf.String()))
	}

	// Figure out which disk and partition to resize,
	// aborts on ambiguity
	diskName, partitionName, diskSizeUnused, err := diskAndPartitionNameToResize(diskutilOutput)
	if err != nil {
		return fmt.Errorf("failed to locate a physical disk to resize: %w", err)
	}

	if diskSizeUnused <= 4096*10 {
		return ErrAlreadyResized
	}

	// Repair the disk whose partition we're going to resize, just in case
	cmd = exec.Command("diskutil", "repairDisk", diskName)

	cmd.Stdin = bytes.NewReader([]byte("yes"))

	stderrBuf = &bytes.Buffer{}
	cmd.Stderr = stderrBuf

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to repair disk %s: %w: %s", diskName, err,
			firstNonEmptyLine(stderrBuf.String()))
	}

	// Finally, resize the partition
	cmd = exec.Command("diskutil", "apfs", "resizeContainer", partitionName, "0")

	stderrBuf = &bytes.Buffer{}
	cmd.Stderr = stderrBuf

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to resize partition %s: %w: %s", partitionName, err,
			firstNonEmptyLine(stderrBuf.String()))
	}

	return nil
}

// diskAndPartitionNameToResize parses "diskutil list -plist" output,
// makes sure there's only one disk on the system and returns its
// name and the name of a single APFS partition or errors otherwise.
func diskAndPartitionNameToResize(input []byte) (string, string, int64, error) {
	var diskUtilOutput DiskUtilOutput

	_, err := plist.Unmarshal(input, &diskUtilOutput)
	if err != nil {
		return "", "", 0, fmt.Errorf("failed to parse \"diskutil list -plist\" output: %w", err)
	}

	type Candidate struct {
		DiskName       string
		DiskSizeUnused int64
		PartitionName  string
	}

	var candidates []Candidate

	for _, diskWithPartitions := range diskUtilOutput.AllDisksAndPartitions {
		unusedSize := diskWithPartitions.Size - lo.SumBy(diskWithPartitions.Partitions, func(partition Partition) int64 {
			return partition.Size
		})

		for _, partition := range diskWithPartitions.Partitions {
			if partition.Content != expectedPartitionContent {
				continue
			}

			candidates = append(candidates, Candidate{
				DiskName:       diskWithPartitions.DeviceIdentifier,
				DiskSizeUnused: unusedSize,
				PartitionName:  partition.DeviceIdentifier,
			})
		}
	}

	if len(candidates) == 0 {
		return "", "", 0, fmt.Errorf("found no disks on which the partition's \"Content\" "+
			"is %q, make sure that the macOS is installed", expectedPartitionContent)
	}

	if len(candidates) > 1 {
		return "", "", 0, fmt.Errorf("found more than one disk on which the partition's \"Content\" "+
			"is %q, please only mount a single disk that contains APFS partitions otherwise it's hard "+
			"to tell on which disk the macOS is installed", expectedPartitionContent)
	}

	return candidates[0].DiskName, candidates[0].PartitionName,
		candidates[0].DiskSizeUnused, nil
}

func firstNonEmptyLine(outputs ...string) string {
	for _, output := range outputs {
		for _, line := range strings.Split(output, "\n") {
			if line != "" {
				return line
			}
		}
	}

	return ""
}
