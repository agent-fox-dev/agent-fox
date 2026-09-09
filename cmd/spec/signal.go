package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// signalCtx creates a context that cancels on SIGINT or SIGTERM.
// On the first signal, the context is cancelled. On a second signal,
// os.Exit(1) is called immediately for force-exit.
func signalCtx(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var once sync.Once
	go func() {
		select {
		case <-sigCh:
			once.Do(func() {
				cancel()
			})
		case <-ctx.Done():
			signal.Stop(sigCh)
			return
		}
		// Wait for a second signal — force-exit.
		select {
		case <-sigCh:
			os.Exit(1)
		case <-ctx.Done():
			signal.Stop(sigCh)
		}
	}()

	return ctx, func() {
		signal.Stop(sigCh)
		cancel()
	}
}
