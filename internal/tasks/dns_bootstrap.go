package tasks

import (
	"context"
	"fmt"
	"time"

	"p2pos/internal/network"
	"p2pos/internal/scheduler"
)

type BootstrapTask struct {
	node     *network.Node
	resolver network.Resolver
}

func NewBootstrapTask(node *network.Node, resolver network.Resolver) *BootstrapTask {
	return &BootstrapTask{
		node:     node,
		resolver: resolver,
	}
}

func (t *BootstrapTask) Name() string {
	return "bootstrap-connect"
}

func (t *BootstrapTask) Interval() time.Duration {
	return 1 * time.Minute
}

func (t *BootstrapTask) RunOnStart() bool {
	return true
}

func (t *BootstrapTask) Run(ctx context.Context) error {
	if len(t.node.Host.Network().Peers()) > 0 {
		fmt.Println("[BOOTSTRAP] Existing peer connection detected, stopping bootstrap discovery")
		return scheduler.ErrTaskCompleted
	}

	candidates, err := t.resolver.Resolve(ctx)
	if err != nil {
		fmt.Printf("[BOOTSTRAP] Resolver warning: %v\n", err)
	}
	if len(candidates) == 0 {
		fmt.Println("[BOOTSTRAP] No candidates resolved")
		return nil
	}

	for _, candidate := range candidates {
		if candidate.ID == t.node.Host.ID() {
			continue
		}
		if err := t.node.Connect(ctx, candidate); err != nil {
			fmt.Printf("[BOOTSTRAP] Failed to connect to %s: %v\n", candidate.ID.String(), err)
			continue
		}
		fmt.Printf("[BOOTSTRAP] Connected to bootstrap peer: %s\n", candidate.ID.String())
		return scheduler.ErrTaskCompleted
	}

	return nil
}
