package tasks

import (
	"context"
	"time"

	"p2pos/internal/network"
)

type PeerSyncTask struct {
	node *network.Node
}

func NewPeerSyncTask(node *network.Node) *PeerSyncTask {
	return &PeerSyncTask{node: node}
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
	return t.node.SyncPeerGraph(ctx)
}
