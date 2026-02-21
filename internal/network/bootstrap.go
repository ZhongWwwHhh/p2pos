package network

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	peersByID := make(map[peerstore.ID]*peerstore.AddrInfo)
	var errs []error

	cfg := r.provider.Get()
	for _, conn := range cfg.InitConnections {
		switch conn.Type {
		case "dns":
			records, err := r.lookupBootstrapTXT(conn.Address)
			if err != nil {
				errs = append(errs, fmt.Errorf("dns %s query failed: %w", conn.Address, err))
				continue
			}
			if len(records) == 0 {
				continue
			}
			for _, record := range records {
				for _, value := range parseTXTRecordValues(record) {
					peerInfo, err := ParseP2PAddr(value)
					if err != nil {
						errs = append(errs, fmt.Errorf("dns %s parse failed for %q: %w", conn.Address, value, err))
						continue
					}
					if peerInfo.ID == r.selfID {
						continue
					}
					mergePeerAddrInfo(peersByID, peerInfo)
				}
			}
		case "multiaddr":
			peerInfo, err := ParseP2PAddr(conn.Address)
			if err != nil {
				errs = append(errs, fmt.Errorf("multiaddr %s parse failed: %w", conn.Address, err))
				continue
			}
			if peerInfo.ID == r.selfID {
				continue
			}
			mergePeerAddrInfo(peersByID, peerInfo)
		default:
			continue
		}
	}

	peers := make([]peerstore.AddrInfo, 0, len(peersByID))
	for _, info := range peersByID {
		peers = append(peers, *info)
	}

	return peers, errors.Join(errs...)
}

func (r *ConfigResolver) lookupBootstrapTXT(domain string) ([]string, error) {
	base := strings.TrimSpace(domain)
	if base == "" {
		return nil, fmt.Errorf("empty dns bootstrap domain")
	}

	// Align with dnsaddr semantics used by browser libp2p:
	// TXT records should live under _dnsaddr.<domain>.
	if !strings.HasPrefix(base, "_dnsaddr.") {
		prefixed := "_dnsaddr." + strings.TrimSuffix(base, ".")
		if records, err := r.dns.LookupTXT(prefixed); err == nil && len(records) > 0 {
			return records, nil
		}
	}

	// Backward-compatible fallback: bare domain TXT.
	return r.dns.LookupTXT(base)
}

func mergePeerAddrInfo(dst map[peerstore.ID]*peerstore.AddrInfo, src *peerstore.AddrInfo) {
	if src == nil || src.ID == "" {
		return
	}
	existing, ok := dst[src.ID]
	if !ok {
		clone := &peerstore.AddrInfo{
			ID:    src.ID,
			Addrs: append([]multiaddr.Multiaddr(nil), src.Addrs...),
		}
		dst[src.ID] = clone
		return
	}
	seen := make(map[string]struct{}, len(existing.Addrs))
	for _, addr := range existing.Addrs {
		seen[addr.String()] = struct{}{}
	}
	for _, addr := range src.Addrs {
		key := addr.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		existing.Addrs = append(existing.Addrs, addr)
	}
}

func parseTXTRecordValues(raw string) []string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil
	}
	if strings.HasPrefix(value, "dnsaddr=") {
		value = strings.TrimSpace(strings.TrimPrefix(value, "dnsaddr="))
	}
	if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") && len(value) >= 2 {
		value = strings.TrimSpace(value[1 : len(value)-1])
	}
	if value == "" {
		return nil
	}
	return []string{value}
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
