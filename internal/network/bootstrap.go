package network

import (
	"context"
	"errors"
	"fmt"

	"p2pos/internal/config"

	peerstore "github.com/libp2p/go-libp2p/core/peer"
	multiaddr "github.com/multiformats/go-multiaddr"
)

type Resolver interface {
	Resolve(ctx context.Context) ([]peerstore.AddrInfo, error)
}

type InitConnectionsProvider interface {
	Get() config.Config
}

type ConfigResolver struct {
	selfID   peerstore.ID
	provider InitConnectionsProvider
	dns      DNSResolver
}

func NewConfigResolver(selfID peerstore.ID, provider InitConnectionsProvider, dns DNSResolver) *ConfigResolver {
	return &ConfigResolver{
		selfID:   selfID,
		provider: provider,
		dns:      dns,
	}
}

func (r *ConfigResolver) Resolve(_ context.Context) ([]peerstore.AddrInfo, error) {
	peers := make([]peerstore.AddrInfo, 0)
	seen := make(map[peerstore.ID]struct{})
	var errs []error

	cfg := r.provider.Get()
	for _, conn := range cfg.InitConnections {
		switch conn.Type {
		case "dns":
			records, err := r.dns.LookupTXT(conn.Address)
			if err != nil {
				errs = append(errs, fmt.Errorf("dns %s query failed: %w", conn.Address, err))
				continue
			}
			if len(records) == 0 {
				continue
			}

			peerInfo, err := ParseP2PAddr(records[0])
			if err != nil {
				errs = append(errs, fmt.Errorf("dns %s parse failed: %w", conn.Address, err))
				continue
			}
			if peerInfo.ID == r.selfID {
				continue
			}
			if _, ok := seen[peerInfo.ID]; ok {
				continue
			}
			seen[peerInfo.ID] = struct{}{}
			peers = append(peers, *peerInfo)
		case "multiaddr":
			peerInfo, err := ParseP2PAddr(conn.Address)
			if err != nil {
				errs = append(errs, fmt.Errorf("multiaddr %s parse failed: %w", conn.Address, err))
				continue
			}
			if peerInfo.ID == r.selfID {
				continue
			}
			if _, ok := seen[peerInfo.ID]; ok {
				continue
			}
			seen[peerInfo.ID] = struct{}{}
			peers = append(peers, *peerInfo)
		default:
			continue
		}
	}

	return peers, errors.Join(errs...)
}

func ParseP2PAddr(multiAddrStr string) (*peerstore.AddrInfo, error) {
	addr, err := multiaddr.NewMultiaddr(multiAddrStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse multiaddr %s: %w", multiAddrStr, err)
	}

	peer, err := peerstore.AddrInfoFromP2pAddr(addr)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to peer address: %w", err)
	}

	return peer, nil
}
