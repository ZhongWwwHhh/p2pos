package network

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"p2pos/internal/events"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	libp2pnet "github.com/libp2p/go-libp2p/core/network"
	peerstore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/ping"
	multiaddr "github.com/multiformats/go-multiaddr"
)

type Node struct {
	Host        host.Host
	PingService *ping.PingService
	Tracker     *Tracker
	bus         *events.Bus
	closeOnce   sync.Once
	closeErr    error
}

type ListenProvider interface {
	ListenAddresses() []string
	NodePrivateKey() crypto.PrivKey
}

func NewNode(cfg ListenProvider, bus *events.Bus) (*Node, error) {
	listenAddrs, err := buildListenMultiaddrs(cfg.ListenAddresses())
	if err != nil {
		return nil, err
	}

	privKey := cfg.NodePrivateKey()
	if privKey == nil {
		return nil, fmt.Errorf("node private key is not initialized")
	}

	hostNode, err := libp2p.New(
		libp2p.ListenAddrStrings(listenAddrs...),
		libp2p.Identity(privKey),
		libp2p.Ping(true),
	)
	if err != nil {
		return nil, err
	}

	n := &Node{
		Host:        hostNode,
		PingService: &ping.PingService{Host: hostNode},
		Tracker:     NewTracker(),
		bus:         bus,
	}
	n.registerConnectionNotifications()
	return n, nil
}

func (n *Node) Close() error {
	n.closeOnce.Do(func() {
		n.closeErr = n.Host.Close()
	})
	return n.closeErr
}

func (n *Node) LogLocalAddrs() error {
	peerInfo := peerstore.AddrInfo{
		ID:    n.Host.ID(),
		Addrs: n.Host.Addrs(),
	}

	addrs, err := peerstore.AddrInfoToP2pAddrs(&peerInfo)
	if err != nil {
		return err
	}

	fmt.Println("[NODE] Local peer address:", addrs)
	return nil
}

func (n *Node) Connect(ctx context.Context, peerInfo peerstore.AddrInfo) error {
	return n.Host.Connect(ctx, peerInfo)
}

func (n *Node) StartBootstrap(ctx context.Context, resolver Resolver, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}

	run := func() bool {
		if len(n.Host.Network().Peers()) > 0 {
			fmt.Println("[BOOTSTRAP] Existing peer connection detected, stopping bootstrap discovery")
			return false
		}

		candidates, err := resolver.Resolve(ctx)
		if err != nil {
			fmt.Printf("[BOOTSTRAP] Resolver warning: %v\n", err)
		}
		if len(candidates) == 0 {
			fmt.Println("[BOOTSTRAP] No candidates resolved")
			return true
		}

		for _, candidate := range candidates {
			if candidate.ID == n.Host.ID() {
				continue
			}
			if err := n.Connect(ctx, candidate); err != nil {
				fmt.Printf("[BOOTSTRAP] Failed to connect to %s: %v\n", candidate.ID.String(), err)
				continue
			}
			fmt.Printf("[BOOTSTRAP] Connected to bootstrap peer: %s\n", candidate.ID.String())
			return false
		}

		return true
	}

	go func() {
		if !run() {
			return
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !run() {
					return
				}
			}
		}
	}()
}

func (n *Node) StartShutdownHandler(ctx context.Context) {
	if n.bus == nil {
		return
	}

	eventCh, cancel := n.bus.Subscribe(16)
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-eventCh:
				if !ok {
					return
				}
				shutdown, ok := evt.(events.ShutdownRequested)
				if !ok {
					continue
				}
				fmt.Printf("[NODE] Shutdown requested (%s), closing host...\n", shutdown.Reason)
				if err := n.Close(); err != nil {
					fmt.Printf("[NODE] Host close failed: %v\n", err)
				}
				return
			}
		}
	}()
}

func (n *Node) registerConnectionNotifications() {
	n.Host.Network().Notify(&libp2pnet.NotifyBundle{
		ConnectedF: func(_ libp2pnet.Network, conn libp2pnet.Conn) {
			n.Tracker.Upsert(remoteAddrInfo(conn.RemotePeer(), conn.RemoteMultiaddr()))
			if n.bus != nil {
				n.bus.Publish(events.PeerConnected{
					PeerID:     conn.RemotePeer().String(),
					RemoteAddr: conn.RemoteMultiaddr().String(),
					At:         time.Now().UTC(),
				})
			}
		},
		DisconnectedF: func(network libp2pnet.Network, conn libp2pnet.Conn) {
			if len(network.ConnsToPeer(conn.RemotePeer())) == 0 {
				n.Tracker.Remove(conn.RemotePeer())
			}
			if n.bus != nil {
				n.bus.Publish(events.PeerDisconnected{
					PeerID:     conn.RemotePeer().String(),
					RemoteAddr: conn.RemoteMultiaddr().String(),
					At:         time.Now().UTC(),
				})
			}
		},
	})
}

func remoteAddrInfo(peerID peerstore.ID, addr multiaddr.Multiaddr) peerstore.AddrInfo {
	return peerstore.AddrInfo{
		ID:    peerID,
		Addrs: []multiaddr.Multiaddr{addr},
	}
}

func buildListenMultiaddrs(listens []string) ([]string, error) {
	addrs := make([]string, 0, len(listens))
	seen := make(map[string]struct{}, len(listens))

	for _, raw := range listens {
		listen := strings.TrimSpace(raw)
		if listen == "" {
			continue
		}

		hosts := []string{}
		port := ""

		if strings.Contains(listen, ":") {
			host, p, err := net.SplitHostPort(listen)
			if err != nil {
				return nil, fmt.Errorf("invalid listen address %q, expected host:port (IPv6 like [::]:4100)", listen)
			}
			port = p
			if host == "" {
				hosts = []string{"0.0.0.0", "::"}
			} else {
				hosts = []string{host}
			}
		} else {
			port = listen
			hosts = []string{"0.0.0.0", "::"}
		}

		for _, host := range hosts {
			ip := net.ParseIP(host)
			if ip == nil {
				return nil, fmt.Errorf("invalid listen host %q", host)
			}

			var addr string
			if ip.To4() != nil {
				addr = fmt.Sprintf("/ip4/%s/tcp/%s", host, port)
			} else {
				addr = fmt.Sprintf("/ip6/%s/tcp/%s", host, port)
			}

			if _, ok := seen[addr]; ok {
				continue
			}
			seen[addr] = struct{}{}
			addrs = append(addrs, addr)
		}
	}

	if len(addrs) == 0 {
		return nil, fmt.Errorf("no valid listen addresses configured")
	}

	return addrs, nil
}
