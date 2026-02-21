package app

import (
	"context"

	"p2pos/internal/config"
	"p2pos/internal/database"
	"p2pos/internal/events"
	"p2pos/internal/logging"
	"p2pos/internal/network"
	"p2pos/internal/scheduler"
)

func Run(_ []string) error {
	logging.Log("APP", "version", map[string]string{
		"version": config.AppVersion,
	})

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
	if err := setupMembership(configStore, netNode); err != nil {
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

	logging.Log("APP", "shutdown", map[string]string{
		"reason": "context_done",
	})
	jobScheduler.Wait()

	return nil
}
