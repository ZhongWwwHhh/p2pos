package network

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"p2pos/internal/events"
	"p2pos/internal/logging"

	libp2pnet "github.com/libp2p/go-libp2p/core/network"
	peerstore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const heartbeatProtocolID = protocol.ID("/p2pos/heartbeat/1.0.0")
const heartbeatWindow = 5 * time.Minute

type heartbeatMessage struct {
	ClusterID string `json:"cluster_id"`
	PeerID    string `json:"peer_id"`
	Timestamp string `json:"ts"`
	Sig       string `json:"sig"`
}

func (n *Node) registerHeartbeatHandler() {
	n.Host.SetStreamHandler(heartbeatProtocolID, func(stream libp2pnet.Stream) {
		defer stream.Close()
		if !n.canUseBusinessProtocols() {
			return
		}

		var msg heartbeatMessage
		if err := json.NewDecoder(stream).Decode(&msg); err != nil {
			logging.Log("STATUS", "heartbeat_decode_failed", map[string]string{
				"reason": err.Error(),
			})
			return
		}
		if err := n.validateHeartbeat(msg); err != nil {
			logging.Log("STATUS", "heartbeat_reject", map[string]string{
				"peer_id": msg.PeerID,
				"reason":  err.Error(),
			})
			return
		}

		remoteAddr := ""
		if stream.Conn() != nil {
			remoteAddr = stream.Conn().RemoteMultiaddr().String()
		}
		if n.bus != nil {
			n.bus.Publish(events.PeerHeartbeat{
				PeerID:     msg.PeerID,
				RemoteAddr: remoteAddr,
				At:         time.Now().UTC(),
			})
		}
	})
}

func (n *Node) BroadcastHeartbeat(ctx context.Context) error {
	if !n.canUseBusinessProtocols() {
		return nil
	}
	if n.privKey == nil {
		return fmt.Errorf("private key not initialized")
	}

	clusterID := n.clusterID()
	ts := time.Now().UTC()
	payload := canonicalHeartbeat(clusterID, n.Host.ID().String(), ts)
	sig, err := n.privKey.Sign(payload)
	if err != nil {
		return err
	}

	msg := heartbeatMessage{
		ClusterID: clusterID,
		PeerID:    n.Host.ID().String(),
		Timestamp: ts.Format(time.RFC3339Nano),
		Sig:       base64.StdEncoding.EncodeToString(sig),
	}

	for _, peerID := range n.Host.Network().Peers() {
		if !n.isMember(peerID.String()) {
			continue
		}
		if _, skip := n.heartbeatUnsupported.Load(peerID); skip {
			continue
		}
		reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := n.sendHeartbeat(reqCtx, peerID, msg); err != nil {
			if strings.Contains(err.Error(), "protocols not supported") {
				n.heartbeatUnsupported.Store(peerID, struct{}{})
				logging.Log("STATUS", "heartbeat_protocol_unsupported", map[string]string{
					"peer_id": peerID.String(),
				})
				cancel()
				continue
			}
			logging.Log("STATUS", "heartbeat_send_failed", map[string]string{
				"peer_id": peerID.String(),
				"reason":  err.Error(),
			})
		}
		cancel()
	}
	return nil
}

func (n *Node) sendHeartbeat(ctx context.Context, peerID peerstore.ID, msg heartbeatMessage) error {
	stream, err := n.Host.NewStream(ctx, peerID, heartbeatProtocolID)
	if err != nil {
		return err
	}
	defer stream.Close()

	return json.NewEncoder(stream).Encode(msg)
}

func (n *Node) validateHeartbeat(msg heartbeatMessage) error {
	if msg.PeerID == "" || msg.Sig == "" || msg.Timestamp == "" {
		return fmt.Errorf("missing fields")
	}
	if !n.isMember(msg.PeerID) {
		return fmt.Errorf("peer not a member")
	}
	clusterID := n.clusterID()
	if clusterID != "" && msg.ClusterID != "" && msg.ClusterID != clusterID {
		return fmt.Errorf("cluster_id mismatch")
	}

	ts, err := time.Parse(time.RFC3339Nano, msg.Timestamp)
	if err != nil {
		return fmt.Errorf("invalid timestamp")
	}
	now := time.Now().UTC()
	if ts.After(now.Add(heartbeatWindow)) || ts.Before(now.Add(-heartbeatWindow)) {
		return fmt.Errorf("timestamp out of window")
	}

	sigBytes, err := base64.StdEncoding.DecodeString(msg.Sig)
	if err != nil {
		return fmt.Errorf("invalid signature encoding")
	}

	id, err := peerstore.Decode(msg.PeerID)
	if err != nil {
		return fmt.Errorf("invalid peer id")
	}
	pub, err := id.ExtractPublicKey()
	if err != nil {
		return fmt.Errorf("extract public key failed")
	}

	payload := canonicalHeartbeat(clusterID, msg.PeerID, ts)
	ok, err := pub.Verify(payload, sigBytes)
	if err != nil || !ok {
		return fmt.Errorf("signature invalid")
	}

	return nil
}

func (n *Node) clusterID() string {
	n.memberMu.RLock()
	manager := n.membership
	n.memberMu.RUnlock()
	if manager == nil {
		return ""
	}
	return manager.Snapshot().ClusterID
}

func canonicalHeartbeat(clusterID, peerID string, ts time.Time) []byte {
	return []byte(fmt.Sprintf("%s|%s|%s", clusterID, peerID, ts.UTC().Format(time.RFC3339Nano)))
}
