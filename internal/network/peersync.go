package network

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"p2pos/internal/events"
	"p2pos/internal/status"

	libp2pnet "github.com/libp2p/go-libp2p/core/network"
	peerstore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const peerExchangeProtocolID = protocol.ID("/p2pos/peer-exchange/1.0.0")
const staleDisconnectedTTL = 10 * time.Minute

type peerExchangeResponse struct {
	Peers   []string             `json:"peers,omitempty"`
	Records []peerExchangeRecord `json:"records,omitempty"`
}

type peerExchangeRecord struct {
	PeerID        string     `json:"peer_id"`
	RemoteAddr    string     `json:"remote_addr,omitempty"`
	LastSeenAt    time.Time  `json:"last_seen_at"`
	LastPingRTTMs *float64   `json:"last_ping_rtt_ms,omitempty"`
	LastPingOK    bool       `json:"last_ping_ok"`
	LastPingAt    *time.Time `json:"last_ping_at,omitempty"`
	Reachability  string     `json:"reachability,omitempty"`
	ObservedBy    string     `json:"observed_by,omitempty"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

func (n *Node) registerPeerExchangeHandler() {
	n.Host.SetStreamHandler(peerExchangeProtocolID, func(stream libp2pnet.Stream) {
		defer stream.Close()

		resp := peerExchangeResponse{
			Peers:   n.collectKnownPeerAddrs(),
			Records: n.collectKnownPeerRecords(),
		}
		if err := json.NewEncoder(stream).Encode(resp); err != nil {
			fmt.Printf("[PEERSYNC] Failed to send peer list: %v\n", err)
		}
	})
}

func (n *Node) SyncPeerGraph(ctx context.Context) error {
	connectedPeers := n.Host.Network().Peers()
	if len(connectedPeers) == 0 {
		return nil
	}
	attempted := make(map[peerstore.ID]struct{})

	for _, remotePeerID := range connectedPeers {
		syncCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		peers, records, err := n.requestPeerAddrs(syncCtx, remotePeerID)
		cancel()
		if err != nil {
			fmt.Printf("[PEERSYNC] Request to %s failed: %v\n", remotePeerID, err)
			continue
		}

		for _, rec := range records {
			if isStaleDisconnectedRecord(rec.Reachability, rec.UpdatedAt) {
				continue
			}
			n.publishObservedRecord(rec)
			if rec.RemoteAddr != "" {
				n.tryConnectDiscovered(ctx, rec.RemoteAddr, attempted)
			}
		}

		for _, addr := range peers {
			n.tryConnectDiscovered(ctx, addr, attempted)
		}
	}

	return nil
}

func (n *Node) SyncPeerAddrs(ctx context.Context, peers []string) error {
	attempted := make(map[peerstore.ID]struct{})
	for _, addr := range peers {
		n.tryConnectDiscovered(ctx, addr, attempted)
	}
	return nil
}

func (n *Node) requestPeerAddrs(ctx context.Context, peerID peerstore.ID) ([]string, []peerExchangeRecord, error) {
	stream, err := n.Host.NewStream(ctx, peerID, peerExchangeProtocolID)
	if err != nil {
		return nil, nil, err
	}
	defer stream.Close()

	var resp peerExchangeResponse
	if err := json.NewDecoder(stream).Decode(&resp); err != nil {
		return nil, nil, err
	}
	return resp.Peers, resp.Records, nil
}

func (n *Node) collectKnownPeerAddrs() []string {
	known := make(map[string]struct{})
	add := func(info peerstore.AddrInfo) {
		addrs, err := peerstore.AddrInfoToP2pAddrs(&info)
		if err != nil {
			return
		}
		for _, addr := range addrs {
			known[addr.String()] = struct{}{}
		}
	}

	add(peerstore.AddrInfo{
		ID:    n.Host.ID(),
		Addrs: n.Host.Addrs(),
	})

	for _, peerID := range n.Host.Network().Peers() {
		info := n.Host.Peerstore().PeerInfo(peerID)
		if info.ID == "" || len(info.Addrs) == 0 {
			continue
		}
		add(info)
	}

	result := make([]string, 0, len(known))
	for addr := range known {
		result = append(result, addr)
	}
	return result
}

func (n *Node) collectKnownPeerRecords() []peerExchangeRecord {
	records := make(map[string]peerExchangeRecord)
	add := func(rec peerExchangeRecord) {
		if rec.PeerID == "" {
			return
		}
		prev, ok := records[rec.PeerID]
		if !ok || rec.UpdatedAt.After(prev.UpdatedAt) {
			records[rec.PeerID] = rec
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snapshot, err := n.localStatus(ctx)
	if err == nil {
		for _, rec := range snapshot {
			ex := statusToExchangeRecord(rec)
			if isStaleDisconnectedRecord(ex.Reachability, ex.UpdatedAt) {
				continue
			}
			add(ex)
		}
	}

	for _, addr := range n.collectKnownPeerAddrs() {
		info, err := ParseP2PAddr(addr)
		if err != nil {
			continue
		}
		if info.ID == n.Host.ID() {
			continue
		}
		now := time.Now().UTC()
		add(peerExchangeRecord{
			PeerID:       info.ID.String(),
			RemoteAddr:   addr,
			LastSeenAt:   now,
			Reachability: "discovered",
			ObservedBy:   n.Host.ID().String(),
			UpdatedAt:    now,
		})
	}

	out := make([]peerExchangeRecord, 0, len(records))
	for _, rec := range records {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].PeerID < out[j].PeerID
	})
	return out
}

func (n *Node) tryConnectDiscovered(ctx context.Context, addr string, attempted map[peerstore.ID]struct{}) {
	info, err := ParseP2PAddr(addr)
	if err != nil {
		return
	}
	if info.ID == n.Host.ID() {
		return
	}
	if attempted != nil {
		if _, ok := attempted[info.ID]; ok {
			return
		}
		attempted[info.ID] = struct{}{}
	}
	if n.bus != nil {
		n.bus.Publish(events.PeerDiscovered{
			PeerID: info.ID.String(),
			Addr:   addr,
			At:     time.Now().UTC(),
		})
	}
	if len(n.Host.Network().ConnsToPeer(info.ID)) > 0 || n.Host.Network().Connectedness(info.ID) == libp2pnet.Connected {
		return
	}
	if err := n.Connect(ctx, *info); err != nil {
		return
	}
	if len(n.Host.Network().ConnsToPeer(info.ID)) > 0 {
		fmt.Printf("[PEERSYNC] Connected discovered peer: %s\n", info.ID)
	}
}

func (n *Node) publishObservedRecord(rec peerExchangeRecord) {
	if n.bus == nil || rec.PeerID == "" {
		return
	}
	lastPingAt := cloneTimePtr(rec.LastPingAt)
	n.bus.Publish(events.PeerStateObserved{
		PeerID:        rec.PeerID,
		RemoteAddr:    rec.RemoteAddr,
		LastSeenAt:    rec.LastSeenAt.UTC(),
		LastPingRTTMs: rec.LastPingRTTMs,
		LastPingOK:    rec.LastPingOK,
		LastPingAt:    lastPingAt,
		Reachability:  rec.Reachability,
		ObservedBy:    rec.ObservedBy,
		ObservedAt:    rec.UpdatedAt.UTC(),
	})
}

func statusToExchangeRecord(rec status.Record) peerExchangeRecord {
	updatedAt := rec.LastSeenAt.UTC()
	if rec.LastPingAt != nil && !rec.LastPingAt.IsZero() {
		updatedAt = rec.LastPingAt.UTC()
	}
	return peerExchangeRecord{
		PeerID:        rec.PeerID,
		RemoteAddr:    rec.LastRemoteAddr,
		LastSeenAt:    rec.LastSeenAt.UTC(),
		LastPingRTTMs: rec.LastPingRTTMs,
		LastPingOK:    rec.LastPingOK,
		LastPingAt:    cloneTimePtr(rec.LastPingAt),
		Reachability:  rec.Reachability,
		ObservedBy:    rec.ObservedBy,
		UpdatedAt:     updatedAt,
	}
}

func cloneTimePtr(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}
	t := v.UTC()
	return &t
}

func isStaleDisconnectedRecord(reachability string, updatedAt time.Time) bool {
	if reachability != "disconnected" {
		return false
	}
	if updatedAt.IsZero() {
		return true
	}
	return time.Since(updatedAt.UTC()) > staleDisconnectedTTL
}
