package app

import (
	"context"
	"fmt"

	"p2pos/internal/config"
	"p2pos/internal/database"
	"p2pos/internal/events"
	"p2pos/internal/network"
	"p2pos/internal/scheduler"
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
	shutdownNotifier := NewBusShutdownRequester(eventBus)
	stopShutdownBridge := startShutdownBridge(ctx, cancel, eventBus, shutdownNotifier)
	defer stopShutdownBridge()

	startRuntimeServices(ctx, eventBus, netNode)

	jobScheduler := scheduler.New()
	if err := registerScheduledTasks(ctx, jobScheduler, netNode, configStore, shutdownNotifier); err != nil {
		return err
	}

	jobScheduler.Start(ctx)
	<-ctx.Done()

	fmt.Println("[NODE] Received shutdown signal, closing...")
	jobScheduler.Wait()

	return nil
}
