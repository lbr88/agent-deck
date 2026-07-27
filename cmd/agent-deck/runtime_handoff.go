package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"

	"github.com/asheshgoplani/agent-deck/internal/update"
)

var (
	errRuntimeHandoffShutdown = errors.New("runtime handoff shutdown")
	errRuntimeExplicitStop    = errors.New("explicit process stop")
)

var (
	processHandoffMu       sync.Mutex
	processHandoff         *update.RuntimeHandoff
	processHandoffShutdown func()
	processHandoffWatch    sync.Once
)

// main wraps the existing command lifecycle so every normal defer/cleanup in
// runAgentDeck completes before a requested same-PID exec. Short-lived CLI
// commands never start the watcher and pay only one executable stat.
func main() {
	handoff, err := update.NewRuntimeHandoff(update.RuntimeHandoffOptions{
		OnRequest: dispatchRuntimeHandoffShutdown,
	})
	if err == nil {
		processHandoffMu.Lock()
		processHandoff = handoff
		processHandoffMu.Unlock()
	}

	runAgentDeckMain()

	if handoff == nil || !handoff.Requested() {
		return
	}
	fmt.Fprintf(os.Stderr, "agent-deck: activating updated binary at %s\n", handoff.Path())
	if err := handoff.Exec(); err != nil {
		if errors.Is(err, update.ErrHandoffCanceled) {
			return
		}
		fmt.Fprintf(os.Stderr, "agent-deck: update handoff failed: %v\n", err)
		os.Exit(1)
	}
}

// watchRuntimeHandoff enables replacement detection for one long-running
// process mode. The callback must initiate that mode's graceful shutdown; the
// wrapper above performs exec only after runAgentDeckMain and all defers return.
func watchRuntimeHandoff(ctx context.Context, shutdown func()) {
	processHandoffMu.Lock()
	processHandoffShutdown = shutdown
	handoff := processHandoff
	processHandoffMu.Unlock()
	if handoff == nil {
		return
	}

	processHandoffWatch.Do(func() {
		go func() {
			if err := handoff.Watch(ctx); err != nil && !errors.Is(err, context.Canceled) {
				fmt.Fprintf(os.Stderr, "agent-deck: update watcher stopped: %v\n", err)
			}
		}()
	})
}

func requestRuntimeHandoff() bool {
	processHandoffMu.Lock()
	handoff := processHandoff
	processHandoffMu.Unlock()
	return handoff != nil && handoff.Request()
}

func runtimeHandoffRequested() bool {
	processHandoffMu.Lock()
	handoff := processHandoff
	processHandoffMu.Unlock()
	return handoff != nil && handoff.Requested()
}

func cancelRuntimeHandoff() bool {
	processHandoffMu.Lock()
	handoff := processHandoff
	processHandoffMu.Unlock()
	return handoff != nil && handoff.Cancel()
}

// runtimeHandoffSignalContext arbitrates replacement against an explicit
// operator/service signal. Whichever cause arrives first owns shutdown; if an
// explicit stop wins, cleanup cancels any concurrently requested re-exec so the
// process stays stopped.
func runtimeHandoffSignalContext(caughtSignals ...os.Signal) (context.Context, func(), func() bool) {
	ctx, cancel := context.WithCancelCause(context.Background())
	signalCtx, stopSignals := signal.NotifyContext(context.Background(), caughtSignals...)
	go func() {
		select {
		case <-signalCtx.Done():
			cancel(errRuntimeExplicitStop)
		case <-ctx.Done():
		}
	}()
	watchRuntimeHandoff(ctx, func() { cancel(errRuntimeHandoffShutdown) })

	isHandoff := func() bool {
		return errors.Is(context.Cause(ctx), errRuntimeHandoffShutdown)
	}
	cleanup := func() {
		stopSignals()
		cancel(errRuntimeExplicitStop)
		if !isHandoff() {
			cancelRuntimeHandoff()
		}
	}
	return ctx, cleanup, isHandoff
}

func dispatchRuntimeHandoffShutdown() {
	processHandoffMu.Lock()
	shutdown := processHandoffShutdown
	processHandoffMu.Unlock()
	if shutdown != nil {
		shutdown()
	}
}
