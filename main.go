package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	peerstore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/ping"
	multiaddr "github.com/multiformats/go-multiaddr"
)

type Config struct {
	InitConnections []struct {
		Type    string `json:"type"`
		Address string `json:"address"`
	} `json:"init_connections"`
}

type PeerTracker struct {
	mu    sync.RWMutex
	peers map[string]peerstore.AddrInfo
}

func (pt *PeerTracker) Add(addr string, peer peerstore.AddrInfo) error {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.peers[addr] = peer
	return nil
}

func (pt *PeerTracker) GetAll() []peerstore.AddrInfo {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	result := make([]peerstore.AddrInfo, 0, len(pt.peers))
	for _, peer := range pt.peers {
		result = append(result, peer)
	}
	return result
}

func loadConfig(configPath string) (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	var cfg Config
	err = json.Unmarshal(data, &cfg)
	return &cfg, err
}

func queryDNSRecords(domain string) ([]string, error) {
	txtRecords, err := net.LookupTXT(domain)
	if err != nil {
		return nil, err
	}
	return txtRecords, nil
}

func startDNSCheckLoop(ctx context.Context, node host.Host, dnsAddress string, peerTracker *PeerTracker) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	checkDNS := func() {
		records, err := queryDNSRecords(dnsAddress)
		if err != nil {
			fmt.Printf("[DNS] Failed to query %s: %v\n", dnsAddress, err)
			return
		}

		if len(records) == 0 {
			fmt.Printf("[DNS] No records found for %s\n", dnsAddress)
			return
		}

		// Parse the first TXT record as multiaddr
		multiAddrStr := records[0]
		addr, err := multiaddr.NewMultiaddr(multiAddrStr)
		if err != nil {
			fmt.Printf("[DNS] Failed to parse multiaddr %s: %v\n", multiAddrStr, err)
			return
		}

		peer, err := peerstore.AddrInfoFromP2pAddr(addr)
		if err != nil {
			fmt.Printf("[DNS] Failed to convert to peer address: %v\n", err)
			return
		}

		// Check if the peer is ourselves
		if peer.ID == node.ID() {
			fmt.Printf("[DNS] Discovered address is ourselves, skipping\n")
			return
		}

		fmt.Printf("[DNS] Discovered peer: %s\n", addr)

		// Try to connect
		if err := node.Connect(ctx, *peer); err != nil {
			fmt.Printf("[DNS] Failed to connect to %s: %v\n", addr, err)
			return
		}

		fmt.Printf("[DNS] Connected to peer: %s\n", addr)
		peerTracker.Add(multiAddrStr, *peer)
	}

	// Initial check
	checkDNS()

	// Periodic checks
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkDNS()
		}
	}
}

func startPingLoop(ctx context.Context, node host.Host, peerTracker *PeerTracker, pingService *ping.PingService) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			peers := peerTracker.GetAll()
			if len(peers) == 0 {
				continue
			}

			for _, peer := range peers {
				go func(p peerstore.AddrInfo) {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()

					ch := pingService.Ping(ctx, p.ID)
					res := <-ch
					fmt.Printf("[PING] Pinged %s: RTT %v\n", p.ID.String(), res.RTT)
				}(peer)
			}
		}
	}
}

func main() {
	// Load configuration
	configPath := "config.json"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		fmt.Printf("Warning: Could not load config file: %v\n", err)
		cfg = &Config{}
	}

	// Start a libp2p node that listens on 4100 on all addresses
	node, err := libp2p.New(
		libp2p.ListenAddrStrings(
			"/ip4/0.0.0.0/tcp/4100",
			"/ip6/::/tcp/4100",
		),
		libp2p.Ping(true),
	)
	if err != nil {
		panic(err)
	}
	defer node.Close()

	// Print node's multiaddr
	peerInfo := peerstore.AddrInfo{
		ID:    node.ID(),
		Addrs: node.Addrs(),
	}
	addrs, err := peerstore.AddrInfoToP2pAddrs(&peerInfo)
	if err != nil {
		panic(err)
	}
	fmt.Println("[NODE] Local peer address:", addrs)

	// Create context for goroutines
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize peer tracker
	peerTracker := &PeerTracker{
		peers: make(map[string]peerstore.AddrInfo),
	}

	// Setup ping service for manual ping handling
	pingService := &ping.PingService{Host: node}

	// Start DNS check loop if configured
	if len(cfg.InitConnections) > 0 {
		for _, conn := range cfg.InitConnections {
			if conn.Type == "dns" {
				go startDNSCheckLoop(ctx, node, conn.Address, peerTracker)
			}
		}
	}

	// Start ping loop
	go startPingLoop(ctx, node, peerTracker, pingService)

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("[NODE] Received shutdown signal, closing...")
	cancel()
}
