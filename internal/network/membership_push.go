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

const membershipPushProtocolID = protocol.ID("/p2pos/membership-push/1.0.0")

type membershipPushResponse struct {
	Applied bool   `json:"applied"`
	Error   string `json:"error,omitempty"`
}

func (n *Node) registerMembershipPushHandler() {
	n.Host.SetStreamHandler(membershipPushProtocolID, func(stream libp2pnet.Stream) {
		defer stream.Close()

		var snapshot membership.Snapshot
		if err := json.NewDecoder(stream).Decode(&snapshot); err != nil {
			_ = json.NewEncoder(stream).Encode(membershipPushResponse{Applied: false, Error: "decode failed"})
			return
		}

		n.memberMu.RLock()
		manager := n.membership
		n.memberMu.RUnlock()
		if manager == nil {
			_ = json.NewEncoder(stream).Encode(membershipPushResponse{Applied: false, Error: "membership not initialized"})
			return
		}

		before := manager.Snapshot().IssuedAt
		if err := manager.Apply(snapshot); err != nil {
			logging.Log("MEMBERSHIP", "reject_snapshot", map[string]string{
				"peer_id": snapshot.IssuerPeerID,
				"reason":  err.Error(),
			})
			_ = json.NewEncoder(stream).Encode(membershipPushResponse{Applied: false, Error: err.Error()})
			return
		}
		after := manager.Snapshot().IssuedAt

		logging.Log("MEMBERSHIP", "apply_snapshot_push", map[string]string{
			"peer_id":   snapshot.IssuerPeerID,
			"issued_at": snapshot.IssuedAt.UTC().Format(time.RFC3339Nano),
			"members":   fmt.Sprintf("%d", len(snapshot.Members)),
		})
		if after.After(before) {
			n.notifyMembershipApplied(manager.Snapshot())
		}
		n.evaluateRuntimeState("membership-push")
		_ = json.NewEncoder(stream).Encode(membershipPushResponse{Applied: true})

		// Propagate only when the pusher is the snapshot issuer to avoid endless relay loops.
		// This covers admin->node direct push from Web Admin and gives one-hop fanout.
		if stream.Conn() != nil && stream.Conn().RemotePeer().String() == snapshot.IssuerPeerID {
			n.fanoutSnapshot(stream.Conn().RemotePeer(), snapshot)
		}
	})
}

func (n *Node) PublishMembershipSnapshot(ctx context.Context, members []string) error {
	if !n.canWriteAdmin() {
		logging.Log("MEMBERSHIP", "publish_denied", map[string]string{
			"state": string(n.RuntimeState()),
		})
		return fmt.Errorf("node not healthy")
	}

	n.memberMu.RLock()
	manager := n.membership
	proof := n.adminProof
	n.memberMu.RUnlock()
	if manager == nil {
		return fmt.Errorf("membership not initialized")
	}
	if proof == nil {
		return fmt.Errorf("admin_proof not configured")
	}
	if err := manager.ValidateAdminProof(*proof, proof.PeerID); err != nil {
		return err
	}

	clusterID := manager.Snapshot().ClusterID
	snapshot := membership.Snapshot{
		ClusterID:    clusterID,
		IssuedAt:     time.Now().UTC(),
		IssuerPeerID: n.Host.ID().String(),
		Members:      members,
		AdminProof:   *proof,
	}
	signed, err := membership.SignSnapshot(n.privKey, snapshot)
	if err != nil {
		return err
	}

	if err := manager.Apply(signed); err != nil {
		return err
	}
	n.notifyMembershipApplied(manager.Snapshot())

	for _, peerID := range n.Host.Network().Peers() {
		if err := n.pushSnapshot(ctx, peerID, signed); err != nil {
			logging.Log("MEMBERSHIP", "push_failed", map[string]string{
				"peer_id": peerID.String(),
				"reason":  err.Error(),
			})
		}
	}

	return nil
}

func (n *Node) fanoutSnapshot(source peerstore.ID, snapshot membership.Snapshot) {
	for _, peerID := range n.Host.Network().Peers() {
		if peerID == source {
			continue
		}
		if err := n.pushSnapshot(context.Background(), peerID, snapshot); err != nil {
			logging.Log("MEMBERSHIP", "fanout_failed", map[string]string{
				"peer_id": peerID.String(),
				"reason":  err.Error(),
			})
		}
	}
}

func (n *Node) pushSnapshot(ctx context.Context, peerID peerstore.ID, snapshot membership.Snapshot) error {
	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	stream, err := n.Host.NewStream(reqCtx, peerID, membershipPushProtocolID)
	if err != nil {
		return err
	}
	defer stream.Close()

	if err := json.NewEncoder(stream).Encode(snapshot); err != nil {
		return err
	}

	var resp membershipPushResponse
	if err := json.NewDecoder(stream).Decode(&resp); err != nil {
		return err
	}
	if !resp.Applied {
		if resp.Error == "" {
			resp.Error = "push rejected"
		}
		return fmt.Errorf(resp.Error)
	}
	return nil
}
