package command

import (
	"errors"
	"github.com/cirruslabs/tart-guest-agent/internal/diskresizer"
	"github.com/cirruslabs/tart-guest-agent/internal/logginglevel"
	"github.com/cirruslabs/tart-guest-agent/internal/tart"
	"github.com/cirruslabs/tart-guest-agent/internal/version"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

var debug bool

func NewRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "tart-guest-agent",
		Short:         "Guest agent for Tart VMs",
		Version:       version.FullVersion,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			if debug {
				logginglevel.Level.SetLevel(zapcore.DebugLevel)
			}

			return nil
		},
		RunE: run,
	}

	return cmd
}

func run(cmd *cobra.Command, args []string) error {
	// Terminate to prevent corruption on systems with disk layouts other than Tart's
	communicationPoint, ok := tart.LocateCommunicationPoint()
	if !ok {
		return unix.Kill(os.Getppid(), syscall.SIGTERM)
	}

	zap.S().Infof("successfully located host communication point %s, proceeding...",
		communicationPoint)

	// Perform disk resizing
	if err := diskresizer.Resize(); err != nil {
		if errors.Is(err, diskresizer.ErrUnsupported) || errors.Is(err, diskresizer.ErrAlreadyResized) {
			zap.S().Infof("skipping disk resizing: %v", err)
		} else {
			zap.S().Warnf("failed to resize disk: %v", err)
		}
	} else {
		zap.S().Infof("sucessfully resized the disk")
	}

	// Wait indefinitely
	<-cmd.Context().Done()

	return cmd.Context().Err()
}
