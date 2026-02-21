package presence

import (
	"context"

	"p2pos/internal/events"
	"p2pos/internal/logging"
)

type PeerRepository interface {
	UpsertLastSeen(ctx context.Context, peerID, remoteAddr, observedBy, reachability string) error
	UpdateReachability(ctx context.Context, peerID, observedBy, reachability string) error
	MergeObservedState(ctx context.Context, state events.PeerStateObserved) error
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
					if err := s.repo.UpsertLastSeen(ctx, e.PeerID, e.RemoteAddr, s.observerID, "online"); err != nil {
						logging.Log("PRESENCE", "update_failed", map[string]string{
							"peer_id": e.PeerID,
							"reason":  err.Error(),
						})
					}
				case events.PeerDisconnected:
					if err := s.repo.UpdateReachability(ctx, e.PeerID, s.observerID, "offline"); err != nil {
						logging.Log("PRESENCE", "update_failed", map[string]string{
							"peer_id": e.PeerID,
							"reason":  err.Error(),
						})
					}
				case events.PeerHeartbeat:
					if err := s.repo.UpsertLastSeen(ctx, e.PeerID, e.RemoteAddr, s.observerID, "online"); err != nil {
						logging.Log("PRESENCE", "heartbeat_failed", map[string]string{
							"peer_id": e.PeerID,
							"reason":  err.Error(),
						})
					}
				case events.PeerStateObserved:
					if err := s.repo.MergeObservedState(ctx, e); err != nil {
						logging.Log("PRESENCE", "merge_failed", map[string]string{
							"peer_id": e.PeerID,
							"reason":  err.Error(),
						})
					}
				}
			}
		}
	}()
}
