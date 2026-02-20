package network

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	libp2pnet "github.com/libp2p/go-libp2p/core/network"
	peerstore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const peerExchangeProtocolID = protocol.ID("/p2pos/peer-exchange/1.0.0")

type peerExchangeResponse struct {
	Peers []string `json:"peers"`
}

func (n *Node) registerPeerExchangeHandler() {
	n.Host.SetStreamHandler(peerExchangeProtocolID, func(stream libp2pnet.Stream) {
		defer stream.Close()

		resp := peerExchangeResponse{
			Peers: n.collectKnownPeerAddrs(),
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

	for _, remotePeerID := range connectedPeers {
		syncCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		peers, err := n.requestPeerAddrs(syncCtx, remotePeerID)
		cancel()
		if err != nil {
			fmt.Printf("[PEERSYNC] Request to %s failed: %v\n", remotePeerID, err)
			continue
		}

		for _, addr := range peers {
			info, err := ParseP2PAddr(addr)
			if err != nil {
				continue
			}
			if info.ID == n.Host.ID() {
				continue
			}
			if n.Host.Network().Connectedness(info.ID) == libp2pnet.Connected {
				continue
			}
			if err := n.Connect(ctx, *info); err != nil {
				continue
			}
			fmt.Printf("[PEERSYNC] Connected discovered peer: %s\n", info.ID)
		}
	}

	return nil
}

func (n *Node) requestPeerAddrs(ctx context.Context, peerID peerstore.ID) ([]string, error) {
	stream, err := n.Host.NewStream(ctx, peerID, peerExchangeProtocolID)
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	var resp peerExchangeResponse
	if err := json.NewDecoder(stream).Decode(&resp); err != nil {
		return nil, err
	}
	return resp.Peers, nil
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
