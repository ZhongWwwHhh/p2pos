package app

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"p2pos/internal/config"
	"p2pos/internal/database"
	"p2pos/internal/network"
	"p2pos/internal/scheduler"
	"p2pos/internal/tasks"
	"p2pos/internal/update"
)

func Run(args []string) error {
	fmt.Printf("[APP] P2POS version: %s\n", update.Version)

	if err := database.Init(); err != nil {
		return err
	}

	nodePrivKey, err := network.LoadOrCreatePrivateKey()
	if err != nil {
		return err
	}

	configPath := "config.json"
	if len(args) > 0 {
		configPath = args[0]
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Printf("Warning: Could not load config file: %v\n", err)
		cfg = &config.Config{}
	}

	netNode, err := network.NewNode(cfg.Listen.Values(), nodePrivKey)
	if err != nil {
		return err
	}
	defer netNode.Close()

	if err := netNode.LogLocalAddrs(); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	jobScheduler := scheduler.New()

	fmt.Println("[APP] Starting auto-update checker...")
	if err := jobScheduler.Register(tasks.NewUpdateCheckTask("ZhongWwwHhh", "Ops-System")); err != nil {
		return err
	}

	for _, conn := range cfg.InitConnections {
		if conn.Type == "dns" {
			if err := jobScheduler.Register(tasks.NewDNSBootstrapTask(netNode.Host, conn.Address, netNode.Tracker)); err != nil {
				return err
			}
		}
	}

	if err := jobScheduler.Register(tasks.NewPingTask(netNode.Tracker, netNode.PingService)); err != nil {
		return err
	}

	jobScheduler.Start(ctx)
	<-ctx.Done()

	fmt.Println("[NODE] Received shutdown signal, closing...")
	jobScheduler.Wait()

	return nil
}
