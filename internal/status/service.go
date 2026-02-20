package status

import (
	"context"
	"time"

	"p2pos/internal/database"
)

type Record struct {
	PeerID         string     `json:"peer_id"`
	LastRemoteAddr string     `json:"last_remote_addr"`
	LastSeenAt     time.Time  `json:"last_seen_at"`
	LastPingRTTMs  *float64   `json:"last_ping_rtt_ms,omitempty"`
	LastPingOK     bool       `json:"last_ping_ok"`
	LastPingAt     *time.Time `json:"last_ping_at,omitempty"`
	Reachability   string     `json:"reachability"`
	ObservedBy     string     `json:"observed_by"`
}

type Repository interface {
	ListPeerStatuses(ctx context.Context) ([]database.Peer, error)
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Snapshot(ctx context.Context) ([]Record, error) {
	if s == nil || s.repo == nil {
		return []Record{}, nil
	}

	peers, err := s.repo.ListPeerStatuses(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]Record, 0, len(peers))
	for _, p := range peers {
		out = append(out, Record{
			PeerID:         p.PeerID,
			LastRemoteAddr: p.LastRemoteAddr,
			LastSeenAt:     p.LastSeenAt,
			LastPingRTTMs:  p.LastPingRTTMs,
			LastPingOK:     p.LastPingOK,
			LastPingAt:     cloneTimePtr(p.LastPingAt),
			Reachability:   p.Reachability,
			ObservedBy:     p.ObservedBy,
		})
	}

	return out, nil
}

func cloneTimePtr(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}
	t := v.UTC()
	return &t
}
