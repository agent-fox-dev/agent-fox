package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/service/api"
	"github.com/agent-fox-dev/agentfox/internal/service/config"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/reconcile"
	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/worker"
)

// The exit codes of the service.
const (
	exitStopped = 0 // it was stopped by SIGINT or SIGTERM
	exitFailed  = 1 // the listener could not be bound, or failed once it was serving
	exitInvalid = 2 // the configuration, the key file or the database is wrong
)

// shutdownGrace is how long the requests in flight get to finish once the service
// is told to stop.
const shutdownGrace = 30 * time.Second

// reconcileInterval is the time between two passes that give back the sandboxes and the sessions that attempts left held;
// the tests make it short.
var reconcileInterval = reconcile.DefaultInterval

// listenFunc binds an address; net.Listen, except in the tests.
type listenFunc func(network, address string) (net.Listener, error)

// startService runs the service until SIGINT or SIGTERM stops it.
func startService(configPath string, logger *slog.Logger) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// A second signal while the requests in flight are drained is taken like the
	// first and changes nothing: the service waits for them, up to the grace period,
	// and exits 0. SIGKILL is how an operator ends it sooner.
	return serve(ctx, configPath, logger, net.Listen)
}

// serve starts the service and runs it until ctx is done or the listener fails.
//
// The start is, in order: the configuration, the key file, the database (which is
// migrated), the server, and the listener. A failure of any of them writes one
// ERROR record, "start failed", and nothing else, and returns 2 for the first
// three (the operator must change a file) and 1 for the listener (the address is
// taken, or not allowed, or not this host's). Nothing is bound before the last
// step, and the database is not created when the configuration or the key is
// wrong. Once the listener is bound, the service says so in a "listening" record
// with the address it is bound to.
//
// When ctx is done, it stops accepting connections, gives the requests in flight
// up to 30 seconds, closes the database and returns 0. When the listener fails
// after the start, it logs the error, closes the database and returns 1.
func serve(ctx context.Context, configPath string, logger *slog.Logger, listen listenFunc) int {
	cfg, err := config.Load(configPath)
	if err != nil {
		return startFailed(logger, exitInvalid, err)
	}
	key, err := secrets.LoadKey(cfg.Secrets.KeyFile)
	if err != nil {
		return startFailed(logger, exitInvalid, err)
	}
	// A signal that comes while the database is opened is taken when it is open.
	st, err := store.Open(context.WithoutCancel(ctx), cfg.Database.Path, key)
	if err != nil {
		return startFailed(logger, exitInvalid, err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			logger.Error("the database could not be closed", "error", err.Error())
		}
	}()
	// The files of the attempts go beside the database; a directory that cannot be
	// made is a file system the operator has to change, like a database that cannot be opened.
	attempts, err := jobs.EnsureAttemptsDir(cfg.Database.Path)
	if err != nil {
		return startFailed(logger, exitInvalid, err)
	}
	document := (&toolio.App{Name: name, Version: version, Commit: commit, BuildTime: buildTime}).VersionDocument()
	hubClient := hub.New(cfg.Hub.URL, document.Version)
	// The worker carries out the attempts: it reads the token of a subscription from the store when
	// an attempt starts, and everything else from hub.
	runner := worker.New(worker.Config{
		Hub: hubClient, Subscriptions: st, Logger: logger,
		Version: document.Version, Commit: document.Commit, BuildTime: document.BuildTime,
	})
	jobService, err := jobs.New(jobs.Config{
		DB: st.DB(), Subscriptions: st, Logger: logger,
		Workers: cfg.Workers.Count, AttemptsDir: attempts, Runner: runner, Retain: cfg.Database.Retain,
	})
	if err != nil {
		return startFailed(logger, exitInvalid, err)
	}
	// The queue is closed before the database is, whichever way the service ends.
	defer func() {
		if err := jobService.Close(); err != nil {
			logger.Error("the job queue could not be stopped", "error", err.Error())
		}
	}()

	srv := api.NewServer(api.Deps{
		Store:        st,
		Jobs:         jobService,
		Hub:          hubClient,
		Logger:       logger,
		Version:      document,
		AuthCacheTTL: cfg.Hub.AuthCacheTTL,
	})

	ln, err := listen("tcp", cfg.Server.Listen)
	if err != nil {
		return startFailed(logger, exitFailed, fmt.Errorf("server.listen %s: %w", cfg.Server.Listen, err))
	}
	if cfg.HubInClear() {
		host := ""
		if u, err := url.Parse(cfg.Hub.URL); err == nil {
			host = u.Host
		}
		logger.Warn("hub.url is plain http to a host that is not a loopback address: caller tokens and workspace tokens cross the link in clear",
			"hub_host", host)
	}
	// The queue starts once the address is bound, so that a start that fails on the
	// address writes its one record and no other.
	if err := jobService.Start(); err != nil {
		ln.Close()
		return startFailed(logger, exitInvalid, err)
	}
	logger.Info("listening", "addr", ln.Addr().String())

	// A pass that gives back what attempts left held is made now, and every 15 minutes. It runs on its own, so that it
	// delays neither the listener nor the queue, and it is stopped, and waited for, before the database is closed.
	reconciler := reconcile.New(reconcile.Config{Subscriptions: st, Hub: hubClient, Jobs: jobService, Logger: logger, Interval: reconcileInterval})
	reconcileCtx, stopReconciling := context.WithCancel(ctx)
	reconciled := make(chan struct{})
	go func() { defer close(reconciled); reconciler.Run(reconcileCtx) }()
	defer func() { stopReconciling(); <-reconciled }()

	serving := make(chan error, 1)
	go func() { serving <- srv.Serve(ln) }()
	select {
	case err := <-serving:
		// Serve returns on its own only when the listener fails.
		logger.Error("the listener failed", "error", err.Error())
		return exitFailed
	case <-ctx.Done():
	}

	grace, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(grace); err != nil {
		logger.Warn("requests in flight did not finish within the grace period", "error", err.Error())
		_ = srv.Close()
	}
	<-serving
	return exitStopped
}

// startFailed writes the one record of a start that failed, and returns the exit code.
func startFailed(logger *slog.Logger, code int, err error) int {
	logger.Error("start failed", "error", err.Error())
	return code
}
