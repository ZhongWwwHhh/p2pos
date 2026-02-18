package discovery

import (
	"context"
	"fmt"
	"net"

	"github.com/libp2p/go-libp2p/core/host"
	peerstore "github.com/libp2p/go-libp2p/core/peer"
	multiaddr "github.com/multiformats/go-multiaddr"
)

type DNSResult int

const (
	DNSResultNoRecord DNSResult = iota
	DNSResultSelf
	DNSResultConnected
)

func QueryDNSRecords(domain string) ([]string, error) {
	txtRecords, err := net.LookupTXT(domain)
	if err != nil {
		return nil, err
	}
	return txtRecords, nil
}

func DiscoverAndConnectDNS(ctx context.Context, node host.Host, dnsAddress string) (DNSResult, peerstore.AddrInfo, string, error) {
	var empty peerstore.AddrInfo

	records, err := QueryDNSRecords(dnsAddress)
	if err != nil {
		return DNSResultNoRecord, empty, "", fmt.Errorf("failed to query %s: %w", dnsAddress, err)
	}

	if len(records) == 0 {
		return DNSResultNoRecord, empty, "", nil
	}

	multiAddrStr := records[0]
	addr, err := multiaddr.NewMultiaddr(multiAddrStr)
	if err != nil {
		return DNSResultNoRecord, empty, "", fmt.Errorf("failed to parse multiaddr %s: %w", multiAddrStr, err)
	}

	peer, err := peerstore.AddrInfoFromP2pAddr(addr)
	if err != nil {
		return DNSResultNoRecord, empty, "", fmt.Errorf("failed to convert to peer address: %w", err)
	}

	if peer.ID == node.ID() {
		return DNSResultSelf, empty, "", nil
	}

	if err := node.Connect(ctx, *peer); err != nil {
		return DNSResultNoRecord, empty, "", fmt.Errorf("failed to connect to %s: %w", addr, err)
	}

	return DNSResultConnected, *peer, multiAddrStr, nil
}
