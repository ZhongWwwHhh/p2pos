package network

import (
	"sync"

	"p2pos/internal/logging"

	libp2pnet "github.com/libp2p/go-libp2p/core/network"
)

type RuntimeState string

const (
	RuntimeStateUnconfigured RuntimeState = "unconfigured"
	RuntimeStateDegraded     RuntimeState = "degraded"
	RuntimeStateHealthy      RuntimeState = "healthy"
)

type stateHolder struct {
	mu    sync.RWMutex
	state RuntimeState
}

func (n *Node) RuntimeState() RuntimeState {
	n.state.mu.RLock()
	defer n.state.mu.RUnlock()
	return n.state.state
}

func (n *Node) setRuntimeState(next RuntimeState, reason string) {
	n.state.mu.Lock()
	prev := n.state.state
	if prev == next {
		n.state.mu.Unlock()
		return
	}
	n.state.state = next
	n.state.mu.Unlock()
	fields := map[string]string{
		"prev":   string(prev),
		"next":   string(next),
		"reason": reason,
	}
	if n.membership != nil {
		snap := n.membership.Snapshot()
		if snap.ClusterID != "" {
			fields["cluster_id"] = snap.ClusterID
		}
	}
	if n.Host != nil {
		fields["peer_id"] = n.Host.ID().String()
	}
	logging.Log("NODE", "runtime_state", fields)
}

func (n *Node) canUseBusinessProtocols() bool {
	return n.RuntimeState() != RuntimeStateUnconfigured
}

func (n *Node) canWriteAdmin() bool {
	return n.RuntimeState() == RuntimeStateHealthy
}

func (n *Node) allowPeer(peerID string) bool {
	if n.RuntimeState() == RuntimeStateUnconfigured {
		return true
	}
	return n.isMember(peerID)
}

func (n *Node) evaluateRuntimeState(reason string) {
	n.memberMu.RLock()
	manager := n.membership
	n.memberMu.RUnlock()
	if manager == nil {
		n.setRuntimeState(RuntimeStateUnconfigured, reason+":membership-nil")
		return
	}

	localID := n.Host.ID().String()
	if !manager.IsMember(localID) {
		n.setRuntimeState(RuntimeStateUnconfigured, reason+":local-not-member")
		return
	}

	snap := manager.Snapshot()
	memberCount := len(snap.Members)
	if memberCount == 0 {
		n.setRuntimeState(RuntimeStateUnconfigured, reason+":member-set-empty")
		return
	}

	online := 1 // local self
	for _, pid := range n.Host.Network().Peers() {
		if n.Host.Network().Connectedness(pid) != libp2pnet.Connected {
			continue
		}
		if manager.IsMember(pid.String()) {
			online++
		}
	}

	if online*2 > memberCount {
		n.setRuntimeState(RuntimeStateHealthy, reason+":quorum")
		return
	}
	n.setRuntimeState(RuntimeStateDegraded, reason+":no-quorum")
}
