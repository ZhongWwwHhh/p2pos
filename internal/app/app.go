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

func Run(_ []string) error {
	fmt.Printf("[APP] P2POS version: %s\n", config.AppVersion)

	if err := database.Init(); err != nil {
		return err
	}

	eventBus := events.NewBus()
	configStore := config.NewStore(eventBus)
	if err := configStore.Init(); err != nil {
		return err
	}

	netNode, err := network.NewNode(configStore, eventBus)
	if err != nil {
		return err
	}
	defer netNode.Close()

	if err := netNode.LogLocalAddrs(); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	shutdownNotifier := NewBusShutdownRequester(eventBus)

	shutdownEvents, unsubscribeShutdownEvents := eventBus.Subscribe(64)
	defer unsubscribeShutdownEvents()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case sig := <-sigChan:
				shutdownNotifier.RequestShutdown(fmt.Sprintf("signal:%s", sig.String()))
			case evt, ok := <-shutdownEvents:
				if !ok {
					return
				}
				shutdown, ok := evt.(events.ShutdownRequested)
				if !ok {
					continue
				}
				fmt.Printf("[APP] Shutdown requested (%s)\n", shutdown.Reason)
				cancel()
				return
			}
		}
	}()

	netNode.StartShutdownHandler(ctx)

	peerPresence := presence.NewService(eventBus, database.NewPeerRepository())
	peerPresence.Start(ctx)

	jobScheduler := scheduler.New()

	fmt.Println("[APP] Starting auto-update checker...")
	updater := update.NewService(configStore, shutdownNotifier)
	if err := jobScheduler.Register(tasks.NewUpdateCheckTask(updater, 3*time.Minute)); err != nil {
		return err
	}

	resolver := network.NewConfigResolver(netNode.Host.ID(), configStore, network.NewNetDNSResolver())
	netNode.StartBootstrap(ctx, resolver, time.Minute)

	if err := jobScheduler.Register(tasks.NewPingTask(netNode.Tracker, netNode.PingService)); err != nil {
		return err
	}

	jobScheduler.Start(ctx)
	<-ctx.Done()

	fmt.Println("[NODE] Received shutdown signal, closing...")
	jobScheduler.Wait()

	return nil
}
