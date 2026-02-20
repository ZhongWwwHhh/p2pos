package tasks

import (
	"context"
	"time"

	"p2pos/internal/database"
	"p2pos/internal/network"
)

type PeerSyncTask struct {
	node *network.Node
	repo PeerStatusRepository
}

type PeerStatusRepository interface {
	ListPeerStatuses(ctx context.Context) ([]database.Peer, error)
	DeleteDisconnectedBefore(ctx context.Context, cutoff time.Time) error
}

func NewPeerSyncTask(node *network.Node, repo PeerStatusRepository) *PeerSyncTask {
	return &PeerSyncTask{
		node: node,
		repo: repo,
	}
}

func (t *PeerSyncTask) Name() string {
	return "peer-sync"
}

func (t *PeerSyncTask) Interval() time.Duration {
	return 30 * time.Second
}

func (t *PeerSyncTask) RunOnStart() bool {
	return false
}

func (t *PeerSyncTask) Run(ctx context.Context) error {
	if t.repo != nil {
		if err := t.repo.DeleteDisconnectedBefore(ctx, time.Now().UTC().Add(-10*time.Minute)); err != nil {
			return err
		}

		peers, err := t.repo.ListPeerStatuses(ctx)
		if err != nil {
			return err
		}
		candidates := make([]string, 0, len(peers))
		for _, p := range peers {
			if p.LastRemoteAddr == "" {
				continue
			}
			candidates = append(candidates, p.LastRemoteAddr)
		}
		if err := t.node.SyncPeerAddrs(ctx, candidates); err != nil {
			return err
		}
	}

	return t.node.SyncPeerGraph(ctx)
}
