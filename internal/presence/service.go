package presence

import (
	"context"
	"fmt"

	"p2pos/internal/events"
)

type PeerRepository interface {
	UpsertLastSeen(ctx context.Context, peerID, remoteAddr, observedBy, reachability string) error
	UpdateReachability(ctx context.Context, peerID, observedBy, reachability string) error
}

type Service struct {
	bus        *events.Bus
	repo       PeerRepository
	observerID string
}

func NewService(bus *events.Bus, repo PeerRepository, observerID string) *Service {
	return &Service{
		bus:        bus,
		repo:       repo,
		observerID: observerID,
	}
}

func (s *Service) Start(ctx context.Context) {
	eventCh, cancel := s.bus.Subscribe(64)
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-eventCh:
				if !ok {
					return
				}
				switch e := evt.(type) {
				case events.PeerConnected:
					if err := s.repo.UpsertLastSeen(ctx, e.PeerID, e.RemoteAddr, s.observerID, "connected"); err != nil {
						fmt.Printf("[PRESENCE] Failed to update peer %s: %v\n", e.PeerID, err)
					}
				case events.PeerDisconnected:
					if err := s.repo.UpdateReachability(ctx, e.PeerID, s.observerID, "disconnected"); err != nil {
						fmt.Printf("[PRESENCE] Failed to update peer reachability %s: %v\n", e.PeerID, err)
					}
				}
			}
		}
	}()
}
