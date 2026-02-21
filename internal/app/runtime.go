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
	"p2pos/internal/logging"
	"p2pos/internal/membership"
	"p2pos/internal/network"
	"p2pos/internal/presence"
	"p2pos/internal/scheduler"
	"p2pos/internal/status"
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
				logging.Log("APP", "shutdown_requested", map[string]string{
					"reason": req.Reason,
				})
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
	peerRepo := database.NewPeerRepository()
	if err := peerRepo.UpsertSelf(ctx, node.Host.ID().String()); err != nil {
		logging.Log("PRESENCE", "init_self_failed", map[string]string{
			"reason": err.Error(),
		})
	}
	peerPresence := presence.NewService(bus, peerRepo, node.Host.ID().String())
	peerPresence.Start(ctx)
	node.SetStatusProvider(status.NewService(peerRepo))
}

func registerScheduledTasks(
	ctx context.Context,
	s *scheduler.Scheduler,
	node *network.Node,
	cfg *config.Store,
	shutdown *BusShutdownRequester,
) error {
	logging.Log("APP", "start_update_checker", nil)
	updater := update.NewService(cfg, shutdown)
	if err := s.Register(tasks.NewUpdateCheckTask(updater, 3*time.Minute)); err != nil {
		return err
	}

	resolver := network.NewConfigResolver(node.Host.ID(), cfg, network.NewNetDNSResolver())
	node.StartBootstrap(ctx, resolver, time.Minute)

	if err := s.Register(tasks.NewMembershipSyncTask(node)); err != nil {
		return err
	}
	if err := s.Register(tasks.NewHeartbeatTask(node)); err != nil {
		return err
	}

	return nil
}

func setupMembership(cfg *config.Store, node *network.Node) error {
	current := cfg.Get()
	manager, err := membership.NewManager(
		current.ClusterID,
		current.SystemPubKey,
		node.Host.ID().String(),
		current.Members,
	)
	if err != nil {
		return err
	}
	proof, ok, err := cfg.AdminProof()
	if err != nil {
		return err
	}
	if ok {
		if proof.PeerID != node.Host.ID().String() {
			return fmt.Errorf("admin_proof peer_id does not match local peer_id")
		}
		if err := manager.ValidateAdminProof(*proof, proof.PeerID); err != nil {
			return err
		}
		node.SetAdminProof(proof)
	}
	node.SetMembershipAppliedHandler(func(snapshot membership.Snapshot) {
		if err := cfg.PersistMembers(snapshot.Members); err != nil {
			logging.Log("CONFIG", "persist_members_failed", map[string]string{
				"reason": err.Error(),
			})
		}
	})
	node.SetMembershipManager(manager)
	return nil
}
