package presence

import (
	"context"
	"fmt"

	"p2pos/internal/events"
)

type PeerRepository interface {
	UpsertLastSeen(ctx context.Context, peerID, remoteAddr string) error
}

type Service struct {
	bus  *events.Bus
	repo PeerRepository
}

func NewService(bus *events.Bus, repo PeerRepository) *Service {
	return &Service{
		bus:  bus,
		repo: repo,
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
				connected, ok := evt.(events.PeerConnected)
				if !ok {
					continue
				}
				if err := s.repo.UpsertLastSeen(ctx, connected.PeerID, connected.RemoteAddr); err != nil {
					fmt.Printf("[PRESENCE] Failed to update peer %s: %v\n", connected.PeerID, err)
				}
			}
		}
	}()
}
