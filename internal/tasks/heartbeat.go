package tasks

import (
	"context"
	"time"

	"p2pos/internal/network"
)

type HeartbeatTask struct {
	node *network.Node
}

func NewHeartbeatTask(node *network.Node) *HeartbeatTask {
	return &HeartbeatTask{node: node}
}

func (t *HeartbeatTask) Name() string {
	return "heartbeat"
}

func (t *HeartbeatTask) Interval() time.Duration {
	return 30 * time.Second
}

func (t *HeartbeatTask) RunOnStart() bool {
	return false
}

func (t *HeartbeatTask) Run(ctx context.Context) error {
	if t.node == nil {
		return nil
	}
	return t.node.BroadcastHeartbeat(ctx)
}
