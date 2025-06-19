package command

import (
	"context"
	"errors"
	"github.com/cirruslabs/tart-guest-agent/internal/diskresizer"
	"github.com/cirruslabs/tart-guest-agent/internal/logginglevel"
	"github.com/cirruslabs/tart-guest-agent/internal/rpc"
	"github.com/cirruslabs/tart-guest-agent/internal/spice/vdagent"
	"github.com/cirruslabs/tart-guest-agent/internal/tart"
	"github.com/cirruslabs/tart-guest-agent/internal/version"
	"github.com/cirruslabs/tart-guest-agent/internal/vsock"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"
	"os"
	"syscall"
	"time"
)

var resizeDisk bool
var runVdagent bool
var runRPC bool

var runDaemon bool
var runAgent bool

var debug bool

const componentFailedTimeout = time.Second

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

	// Individual components
	cmd.Flags().BoolVar(&resizeDisk, "resize-disk", false, "resize disk")
	cmd.Flags().BoolVar(&runVdagent, "run-vdagent", false, "run vdagent")
	cmd.Flags().BoolVar(&runRPC, "run-rpc", false, "run RPC service (currently required "+
		"to support \"tart exec\" functionality)")

	// Component groups
	cmd.Flags().BoolVar(&runDaemon, "run-daemon", false, "identical to running the agent"+
		"with \"--resize-disk\" command-line argument")
	cmd.Flags().BoolVar(&runAgent, "run-agent", false, "identical to running the agent "+
		"with \"--run-vdagent\" and \"--run-rpc\" command-line arguments")

	cmd.Flags().BoolVar(&debug, "debug", false, "enable debug logging")

	return cmd
}

func run(cmd *cobra.Command, args []string) error {
	// Component groups automatically enable certain individual components
	if runDaemon {
		resizeDisk = true
	}

	if runAgent {
		runVdagent = true
		runRPC = true
	}

	// Terminate to prevent corruption on systems with disk layouts other than Tart's
	communicationPoint, ok := tart.LocateCommunicationPoint()
	if !ok {
		return unix.Kill(os.Getppid(), syscall.SIGTERM)
	}

	zap.S().Infof("successfully located host communication point %s, proceeding...",
		communicationPoint)

	// Perform disk resizing
	if resizeDisk {
		zap.S().Info("attempting to resize disk...")

		if err := diskresizer.Resize(); err != nil {
			if errors.Is(err, diskresizer.ErrUnsupported) || errors.Is(err, diskresizer.ErrAlreadyResized) {
				zap.S().Infof("skipping disk resizing: %v", err)
			} else {
				zap.S().Warnf("failed to resize disk: %v", err)
			}
		} else {
			zap.S().Infof("successfully resized the disk")
		}
	}

	group, ctx := errgroup.WithContext(cmd.Context())

	if runVdagent {
		group.Go(func() error {
			for {
				if err := runVdagentOnce(ctx); err != nil {
					return err
				}

				select {
				case <-time.After(componentFailedTimeout):
					continue
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		})
	}

	if runRPC {
		group.Go(func() error {
			for {
				if err := runRPCOnce(ctx); err != nil {
					return err
				}

				select {
				case <-time.After(componentFailedTimeout):
					continue
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		})
	}

	return group.Wait()
}

func runVdagentOnce(ctx context.Context) error {
	zap.S().Infof("initializing vdagent...")

	vdAgent, err := vdagent.New()
	if err != nil {
		zap.S().Errorf("failed to initialize vdagent: %v", err)

		return nil
	}
	defer vdAgent.Close()

	zap.S().Infof("running vdagent...")

	if err := vdAgent.Run(ctx); err != nil {
		zap.S().Errorf("failed to run vdagent: %v", err)

		return nil
	}

	return nil
}

func runRPCOnce(ctx context.Context) error {
	zap.S().Infof("initializing RPC server...")

	listener, err := vsock.Listen(8080)
	if err != nil {
		zap.S().Errorf("RPC server failed to listen on AF_VSOCK port 8080: %v", err)

		return nil
	}
	defer listener.Close()

	rpcServer, err := rpc.New(listener)
	if err != nil {
		zap.S().Errorf("failed to initialize RPC server: %v", err)

		return nil
	}

	zap.S().Info("running RPC server on AF_VSOCK port 8080...")

	if err := rpcServer.Run(); err != nil {
		zap.S().Errorf("failed to run RPC server: %v", err)

		return nil
	}

	return nil
}
