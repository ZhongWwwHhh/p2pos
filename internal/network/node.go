package network

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"p2pos/internal/events"
	"p2pos/internal/status"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	libp2pevent "github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	libp2pnet "github.com/libp2p/go-libp2p/core/network"
	peerstore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/host/autorelay"
	"github.com/libp2p/go-libp2p/p2p/protocol/ping"
	multiaddr "github.com/multiformats/go-multiaddr"
)

type Node struct {
	Host        host.Host
	PingService *ping.PingService
	Tracker     *Tracker
	bus         *events.Bus
	statusMu    sync.RWMutex
	status      StatusProvider
	closeOnce   sync.Once
	closeErr    error
}

type ListenProvider interface {
	ListenAddresses() []string
	NodePrivateKey() crypto.PrivKey
	NetworkMode() string
}

type StatusProvider interface {
	Snapshot(ctx context.Context) ([]status.Record, error)
}

func NewNode(cfg ListenProvider, bus *events.Bus) (*Node, error) {
	tuneQUICUDPBuffer()

	listenAddrs, err := buildListenMultiaddrs(cfg.ListenAddresses())
	if err != nil {
		return nil, err
	}

	privKey := cfg.NodePrivateKey()
	if privKey == nil {
		return nil, fmt.Errorf("node private key is not initialized")
	}

	enablePublicService := shouldEnablePublicService(cfg.NetworkMode())

	var hostRef struct {
		mu sync.RWMutex
		h  host.Host
	}
	getHost := func() host.Host {
		hostRef.mu.RLock()
		defer hostRef.mu.RUnlock()
		return hostRef.h
	}

	relayPeerSource := func(ctx context.Context, num int) <-chan peerstore.AddrInfo {
		ch := make(chan peerstore.AddrInfo, num)
		go func() {
			defer close(ch)

			h := getHost()
			if h == nil {
				return
			}

			sent := 0
			for _, peerID := range h.Network().Peers() {
				if sent >= num {
					return
				}
				if h.Network().Connectedness(peerID) != libp2pnet.Connected {
					continue
				}
				info := h.Peerstore().PeerInfo(peerID)
				if info.ID == "" || len(info.Addrs) == 0 {
					continue
				}

				select {
				case <-ctx.Done():
					return
				case ch <- info:
					sent++
				}
			}
		}()
		return ch
	}

	opts := []libp2p.Option{
		libp2p.ListenAddrStrings(listenAddrs...),
		libp2p.Identity(privKey),
		libp2p.Ping(true),
		libp2p.NATPortMap(),
		libp2p.EnableAutoRelayWithPeerSource(autorelay.PeerSource(relayPeerSource)),
		libp2p.EnableHolePunching(),
	}
	if enablePublicService {
		opts = append(opts, libp2p.EnableNATService(), libp2p.EnableRelayService())
	}

	hostNode, err := libp2p.New(opts...)
	if err != nil {
		return nil, err
	}
	hostRef.mu.Lock()
	hostRef.h = hostNode
	hostRef.mu.Unlock()

	n := &Node{
		Host:        hostNode,
		PingService: &ping.PingService{Host: hostNode},
		Tracker:     NewTracker(),
		bus:         bus,
	}
	if enablePublicService {
		fmt.Println("[NODE] Network mode: public (NATPortMap, AutoRelay, HolePunching, NATService, RelayService)")
	} else {
		fmt.Println("[NODE] Network mode: private (NATPortMap, AutoRelay, HolePunching)")
	}
	n.registerConnectionNotifications()
	n.registerPeerExchangeHandler()
	n.registerStatusHandler()
	n.startReachabilityWatcher()
	return n, nil
}

func shouldEnablePublicService(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "public":
		return true
	case "private":
		return false
	default:
		if hasPublicIPv4() {
			fmt.Println("[NODE] network_mode=auto detected public IPv4, enabling public services")
			return true
		}
		fmt.Println("[NODE] network_mode=auto no public IPv4 detected, using private mode")
		return false
	}
}

func hasPublicIPv4() bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}

	cgnat := netip.MustParsePrefix("100.64.0.0/10")
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP == nil {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil {
				continue
			}
			if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			if cgnat.Contains(netip.AddrFrom4([4]byte{ip[0], ip[1], ip[2], ip[3]})) {
				continue
			}
			return true
		}
	}

	return false
}

func (n *Node) startReachabilityWatcher() {
	sub, err := n.Host.EventBus().Subscribe(new(libp2pevent.EvtLocalReachabilityChanged))
	if err != nil {
		fmt.Printf("[NODE] Reachability subscribe failed: %v\n", err)
		return
	}

	go func() {
		defer sub.Close()
		for evt := range sub.Out() {
			ev, ok := evt.(libp2pevent.EvtLocalReachabilityChanged)
			if !ok {
				continue
			}
			fmt.Printf("[NODE] AutoNAT reachability changed: %s\n", ev.Reachability.String())
		}
	}()
}

func tuneQUICUDPBuffer() {
	if runtime.GOOS != "linux" {
		return
	}

	settings := []string{
		"net.core.rmem_max=7340032",
		"net.core.wmem_max=7340032",
		"net.core.rmem_default=7340032",
		"net.core.wmem_default=7340032",
	}

	for _, kv := range settings {
		out, err := exec.Command("sysctl", "-w", kv).CombinedOutput()
		if err != nil {
			fmt.Printf("[NODE] QUIC UDP buffer tune failed (%s): %v (%s)\n", kv, err, strings.TrimSpace(string(out)))
		}
	}
}

func (n *Node) Close() error {
	n.closeOnce.Do(func() {
		n.closeErr = n.Host.Close()
	})
	return n.closeErr
}

func (n *Node) SetStatusProvider(provider StatusProvider) {
	n.statusMu.Lock()
	n.status = provider
	n.statusMu.Unlock()
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

			var tcpAddr string
			var quicAddr string
			if ip.To4() != nil {
				tcpAddr = fmt.Sprintf("/ip4/%s/tcp/%s", host, port)
				quicAddr = fmt.Sprintf("/ip4/%s/udp/%s/quic-v1", host, port)
			} else {
				tcpAddr = fmt.Sprintf("/ip6/%s/tcp/%s", host, port)
				quicAddr = fmt.Sprintf("/ip6/%s/udp/%s/quic-v1", host, port)
			}

			for _, addr := range []string{tcpAddr, quicAddr} {
				if _, ok := seen[addr]; ok {
					continue
				}
				seen[addr] = struct{}{}
				addrs = append(addrs, addr)
			}
		}
	}

	if len(addrs) == 0 {
		return nil, fmt.Errorf("no valid listen addresses configured")
	}

	return addrs, nil
}
