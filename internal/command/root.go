package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/cirruslabs/tart-guest-agent/internal/diskresizer"
	"github.com/cirruslabs/tart-guest-agent/internal/doctor"
	"github.com/cirruslabs/tart-guest-agent/internal/logginglevel"
	"github.com/cirruslabs/tart-guest-agent/internal/rpc"
	"github.com/cirruslabs/tart-guest-agent/internal/spice/vdagent"
	"github.com/cirruslabs/tart-guest-agent/internal/tart"
	"github.com/cirruslabs/tart-guest-agent/internal/tray"
	"github.com/cirruslabs/tart-guest-agent/internal/ui"
	"github.com/cirruslabs/tart-guest-agent/internal/version"
	"github.com/cirruslabs/tart-guest-agent/internal/vsock"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"
)

var resizeDisk bool
var runVdagent bool
var runRPC bool
var runTray bool

var runDaemon bool
var runAgent bool
var runDoctor bool
var runNotifications bool
var runSettings bool

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

	// Doctor subcommand
	var enableSelfTest bool
	var enableNotify bool
	doctorCmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run guest agent environment and capability diagnostics",
		Run: func(_ *cobra.Command, _ []string) {
			os.Exit(doctor.PrintDoctorReport(enableSelfTest, enableNotify))
		},
	}
	doctorCmd.Flags().BoolVarP(&enableSelfTest, "self-test", "s", false, "run active read/write clipboard loopback self-test")
	doctorCmd.Flags().BoolVarP(&enableNotify, "notify", "n", false, "send desktop notification with diagnostic results")
	cmd.AddCommand(doctorCmd)

	// Tray subcommand
	trayCmd := &cobra.Command{
		Use:   "tray",
		Short: "Run guest agent system tray / status bar service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return tray.New().Run(cmd.Context())
		},
	}
	cmd.AddCommand(trayCmd)

	// Notifications panel subcommand
	notificationsCmd := &cobra.Command{
		Use:   "notifications",
		Short: "Open guest agent notifications and recent activity panel",
		RunE: func(_ *cobra.Command, _ []string) error {
			return ui.ShowNotificationsPanel()
		},
	}
	cmd.AddCommand(notificationsCmd)

	// Settings dialog subcommand
	settingsCmd := &cobra.Command{
		Use:   "settings",
		Short: "Open guest agent interactive settings and preferences dialog",
		RunE: func(_ *cobra.Command, _ []string) error {
			return ui.ShowSettingsDialog()
		},
	}
	cmd.AddCommand(settingsCmd)

	// Individual components
	cmd.Flags().BoolVar(&resizeDisk, "resize-disk", false, "resize disk")
	cmd.Flags().BoolVar(&runVdagent, "run-vdagent", false, "run vdagent")
	cmd.Flags().BoolVar(&runRPC, "run-rpc", false, "run RPC service (currently required "+
		"to support \"tart exec\" functionality)")
	cmd.Flags().BoolVar(&runTray, "run-tray", false, "run system tray / status bar service")
	cmd.Flags().BoolVar(&runDoctor, "doctor", false, "run guest agent environment and capability diagnostics")
	cmd.Flags().BoolVar(&runNotifications, "notifications", false, "open recent notifications and activity panel")
	cmd.Flags().BoolVar(&runSettings, "settings", false, "open settings and preferences dialog")

	// Component groups
	cmd.Flags().BoolVar(&runDaemon, "run-daemon", false, "identical to running the agent"+
		"with \"--resize-disk\" command-line argument")
	cmd.Flags().BoolVar(&runAgent, "run-agent", false, "identical to running the agent "+
		"with \"--run-vdagent\" and \"--run-rpc\" command-line arguments")

	cmd.Flags().BoolVar(&debug, "debug", false, "enable debug logging")

	return cmd
}

func run(cmd *cobra.Command, args []string) error {
	if runDoctor {
		os.Exit(doctor.PrintDoctorReport(false, false))
	}
	if runNotifications {
		return ui.ShowNotificationsPanel()
	}
	if runSettings {
		return ui.ShowSettingsDialog()
	}
	// Component groups automatically enable certain individual components
	if runDaemon {
		resizeDisk = true
	}

	if runAgent {
		runVdagent = true
		runRPC = true
	}

	if !resizeDisk && !runVdagent && !runRPC && !runTray {
		return fmt.Errorf("at least one component must be enabled")
	}

	// Terminate to prevent disk corruption on macOS guests
	// with disk layouts other than provided by Tart
	if runtime.GOOS == "darwin" {
		version, ok := tart.Version()
		if !ok {
			if p, err := os.FindProcess(os.Getppid()); err == nil {
				_ = p.Signal(syscall.SIGTERM)
			}
			return errors.New("failed to identify Tart version on macOS guest")
		}

		zap.S().Infof("running on Tart %s, proceeding...", version.String())
	}

	// Perform disk resizing
	if resizeDisk {
		zap.S().Infof("resizing the disk...")

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
			exponentialBackoff := backoff.NewExponentialBackOff()

			for {
				if err := runVdagentOnce(ctx); err != nil {
					return err
				}

				select {
				case <-time.After(exponentialBackoff.NextBackOff()):
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

	if runTray {
		group.Go(func() error {
			return tray.New().Run(ctx)
		})
	}

	if runVdagent || runAgent {
		tray.EmitStartupToast()
	}

	// When running in daemon or agent mode, wait indefinitely until terminated
	if runDaemon || runAgent {
		group.Go(func() error {
			<-ctx.Done()

			return ctx.Err()
		})
	}

	return group.Wait()
}

func runVdagentOnce(ctx context.Context) error {
	zap.S().Infof("initializing vdagent...")

	vdAgent, err := vdagent.New()
	if err != nil {
		zap.S().Errorf("failed to initialize vdagent: %v", err)
		if !runDaemon && !runAgent {
			return err
		}
		return nil
	}
	defer vdAgent.Close()

	zap.S().Infof("running vdagent...")

	if err := vdAgent.Run(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		zap.S().Errorf("failed to run vdagent: %v", err)
		if !runDaemon && !runAgent {
			return err
		}
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

	if err := rpcServer.Run(ctx); err != nil {
		zap.S().Errorf("failed to run RPC server: %v", err)

		return nil
	}

	return nil
}
