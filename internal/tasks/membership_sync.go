package tasks

import (
	"context"
	"time"

	"p2pos/internal/network"
)

type MembershipSyncTask struct {
	node *network.Node
}

func NewMembershipSyncTask(node *network.Node) *MembershipSyncTask {
	return &MembershipSyncTask{node: node}
}

func (t *MembershipSyncTask) Name() string {
	return "membership-sync"
}

func (t *MembershipSyncTask) Interval() time.Duration {
	return 30 * time.Second
}

func (t *MembershipSyncTask) RunOnStart() bool {
	return false
}

func (t *MembershipSyncTask) Run(ctx context.Context) error {
	if t.node == nil {
		return nil
	}
	return t.node.SyncMembership(ctx)
}
