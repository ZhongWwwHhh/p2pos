package network

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"p2pos/internal/logging"
	"p2pos/internal/membership"

	libp2pnet "github.com/libp2p/go-libp2p/core/network"
	peerstore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const membershipProtocolID = protocol.ID("/p2pos/membership/1.0.0")

type membershipResponse struct {
	Snapshot membership.Snapshot `json:"snapshot"`
	Error    string              `json:"error,omitempty"`
}

func (n *Node) registerMembershipHandler() {
	n.Host.SetStreamHandler(membershipProtocolID, func(stream libp2pnet.Stream) {
		defer stream.Close()

		resp := membershipResponse{}
		snap, ok := n.membershipSnapshot()
		if !ok {
			resp.Error = "membership not initialized"
		} else {
			resp.Snapshot = snap
		}

		if err := json.NewEncoder(stream).Encode(resp); err != nil {
			fmt.Printf("[MEMBERSHIP] Failed to write response: %v\n", err)
		}
	})
}

func (n *Node) membershipSnapshot() (membership.Snapshot, bool) {
	n.memberMu.RLock()
	manager := n.membership
	n.memberMu.RUnlock()
	if manager == nil {
		return membership.Snapshot{}, false
	}
	return manager.Snapshot(), true
}

func (n *Node) fetchMembershipSnapshot(ctx context.Context, peerID peerstore.ID) (membership.Snapshot, error) {
	stream, err := n.Host.NewStream(ctx, peerID, membershipProtocolID)
	if err != nil {
		return membership.Snapshot{}, err
	}
	defer stream.Close()

	var resp membershipResponse
	if err := json.NewDecoder(stream).Decode(&resp); err != nil {
		return membership.Snapshot{}, err
	}
	if resp.Error != "" {
		return membership.Snapshot{}, fmt.Errorf(resp.Error)
	}
	return resp.Snapshot, nil
}

func (n *Node) SyncMembership(ctx context.Context) error {
	n.memberMu.RLock()
	manager := n.membership
	n.memberMu.RUnlock()
	if manager == nil {
		return nil
	}
	if n.Host == nil {
		return nil
	}

	for _, peerID := range n.Host.Network().Peers() {
		reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		snapshot, err := n.fetchMembershipSnapshot(reqCtx, peerID)
		cancel()
		if err != nil {
			continue
		}
		if snapshot.Sig == "" || snapshot.IssuerPeerID == "" {
			continue
		}

		before := manager.Snapshot().IssuedAt
		if err := manager.Apply(snapshot); err != nil {
			logging.Log("MEMBERSHIP", "reject_snapshot", map[string]string{
				"peer_id": peerID.String(),
				"reason":  err.Error(),
			})
			continue
		}
		after := manager.Snapshot().IssuedAt
		if after.After(before) {
			logging.Log("MEMBERSHIP", "apply_snapshot", map[string]string{
				"peer_id":   peerID.String(),
				"issued_at": after.UTC().Format(time.RFC3339Nano),
				"members":   fmt.Sprintf("%d", len(manager.Snapshot().Members)),
			})
		}
	}
	n.evaluateRuntimeState("membership-sync")
	return nil
}
