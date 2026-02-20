package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"p2pos/internal/config"
	"p2pos/internal/database"
	"p2pos/internal/events"
	"p2pos/internal/network"
	"p2pos/internal/presence"
	"p2pos/internal/scheduler"
	"p2pos/internal/tasks"
	"p2pos/internal/update"
)

func startShutdownBridge(ctx context.Context, cancel context.CancelFunc, bus *events.Bus, shutdown *BusShutdownRequester) func() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	shutdownEvents, unsubscribe := bus.Subscribe(64)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case sig := <-sigChan:
				shutdown.RequestShutdown(fmt.Sprintf("signal:%s", sig.String()))
			case evt, ok := <-shutdownEvents:
				if !ok {
					return
				}
				req, ok := evt.(events.ShutdownRequested)
				if !ok {
					continue
				}
				fmt.Printf("[APP] Shutdown requested (%s)\n", req.Reason)
				cancel()
				return
			}
		}
	}()

	return func() {
		signal.Stop(sigChan)
		unsubscribe()
	}
}

func startRuntimeServices(ctx context.Context, bus *events.Bus, node *network.Node) {
	node.StartShutdownHandler(ctx)
	peerPresence := presence.NewService(bus, database.NewPeerRepository())
	peerPresence.Start(ctx)
}

func registerScheduledTasks(
	ctx context.Context,
	s *scheduler.Scheduler,
	node *network.Node,
	cfg *config.Store,
	shutdown *BusShutdownRequester,
) error {
	fmt.Println("[APP] Starting auto-update checker...")
	updater := update.NewService(cfg, shutdown)
	if err := s.Register(tasks.NewUpdateCheckTask(updater, 3*time.Minute)); err != nil {
		return err
	}

	resolver := network.NewConfigResolver(node.Host.ID(), cfg, network.NewNetDNSResolver())
	node.StartBootstrap(ctx, resolver, time.Minute)

	if err := s.Register(tasks.NewPingTask(node.Tracker, node.PingService)); err != nil {
		return err
	}

	return nil
}
