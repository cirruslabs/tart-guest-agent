package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/cirruslabs/tart-guest-agent/internal/command"
	"github.com/cirruslabs/tart-guest-agent/internal/logginglevel"
	"go.uber.org/zap"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if !mainImpl() {
		os.Exit(1)
	}
}

func mainImpl() bool {
	// Set up a signal-interruptible context
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Initialize logger
	loggerConfig := zap.NewProductionConfig()
	loggerConfig.Level = logginglevel.Level
	logger, err := loggerConfig.Build()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)

		return false
	}
	defer func() {
		_ = logger.Sync()
	}()

	// Replace zap.L() and zap.S() to avoid
	// propagating the *zap.Logger by hand
	zap.ReplaceGlobals(logger)

	if err := command.NewRootCommand().ExecuteContext(ctx); err != nil {
		logger.Sugar().Error(err)

		// Do not treat context cancellation as an error
		if errors.Is(err, context.Canceled) {
			return true
		}

		return false
	}

	return true
}
