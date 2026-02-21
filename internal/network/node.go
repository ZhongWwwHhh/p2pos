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
	"p2pos/internal/logging"
	"p2pos/internal/membership"
	"p2pos/internal/status"

	"github.com/caddyserver/certmagic"
	p2pforge "github.com/ipshipyard/p2p-forge/client"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	libp2pevent "github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	libp2pnet "github.com/libp2p/go-libp2p/core/network"
	peerstore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/host/autorelay"
	"github.com/libp2p/go-libp2p/p2p/protocol/ping"
	libp2pquic "github.com/libp2p/go-libp2p/p2p/transport/quic"
	libp2ptcp "github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"github.com/libp2p/go-libp2p/p2p/transport/websocket"
	multiaddr "github.com/multiformats/go-multiaddr"
)

type Node struct {
	Host                 host.Host
	PingService          *ping.PingService
	Tracker              *Tracker
	bus                  *events.Bus
	memberMu             sync.RWMutex
	membership           *membership.Manager
	onMembershipApplied  func(snapshot membership.Snapshot)
	heartbeatUnsupported sync.Map
	statusUnsupported    sync.Map
	state                stateHolder
	statusMu             sync.RWMutex
	status               StatusProvider
	privKey              crypto.PrivKey
	adminProof           *membership.AdminProof
	autoTLSMgr           *p2pforge.P2PForgeCertMgr
	closeOnce            sync.Once
	closeErr             error
}

type ListenProvider interface {
	ListenAddresses() []string
	NodePrivateKey() crypto.PrivKey
	NetworkMode() string
	AutoTLSMode() string
	AutoTLSUserEmail() string
	AutoTLSCacheDir() string
	AutoTLSPort() int
	AutoTLSForgeAuth() string
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
	enablePublicService := shouldEnablePublicService(cfg.NetworkMode())
	wsOptions := []interface{}{}
	var autoTLSMgr *p2pforge.P2PForgeCertMgr
	switch strings.ToLower(strings.TrimSpace(cfg.AutoTLSMode())) {
	case "on":
		autoTLSMgr, err = createAutoTLSManager(cfg, &listenAddrs, &wsOptions, true)
	case "auto":
		if enablePublicService {
			autoTLSMgr, err = createAutoTLSManager(cfg, &listenAddrs, &wsOptions, false)
		}
	case "off":
		// disabled explicitly
	default:
		if enablePublicService {
			autoTLSMgr, err = createAutoTLSManager(cfg, &listenAddrs, &wsOptions, false)
		}
	}
	if err != nil {
		return nil, err
	}
	if autoTLSMgr == nil {
		logging.Log("NODE", "autotls_disabled", map[string]string{
			"mode": cfg.AutoTLSMode(),
		})
	}

	privKey := cfg.NodePrivateKey()
	if privKey == nil {
		return nil, fmt.Errorf("node private key is not initialized")
	}

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
		libp2p.Transport(libp2ptcp.NewTCPTransport),
		libp2p.Transport(libp2pquic.NewTransport),
		libp2p.Transport(websocket.New, wsOptions...),
	}
	if autoTLSMgr != nil {
		opts = append(opts, libp2p.AddrsFactory(autoTLSMgr.AddressFactory()))
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
		privKey:     privKey,
		autoTLSMgr:  autoTLSMgr,
		state: stateHolder{
			state: RuntimeStateUnconfigured,
		},
	}
	if autoTLSMgr != nil {
		autoTLSMgr.ProvideHost(hostNode)
		if err := autoTLSMgr.Start(); err != nil {
			hostNode.Close()
			return nil, err
		}
	}
	if enablePublicService {
		logging.Log("NODE", "network_mode", map[string]string{
			"mode": "public",
		})
	} else {
		logging.Log("NODE", "network_mode", map[string]string{
			"mode": "private",
		})
	}
	n.registerConnectionNotifications()
	n.registerMembershipHandler()
	n.registerMembershipPushHandler()
	n.registerHeartbeatHandler()
	n.registerStatusHandler()
	n.startReachabilityWatcher()
	return n, nil
}

func createAutoTLSManager(cfg ListenProvider, listenAddrs *[]string, wsOptions *[]interface{}, force bool) (*p2pforge.P2PForgeCertMgr, error) {
	autoTLSOpts := []p2pforge.P2PForgeCertMgrOptions{
		p2pforge.WithUserEmail(cfg.AutoTLSUserEmail()),
		p2pforge.WithCertificateStorage(&certmagic.FileStorage{
			Path: cfg.AutoTLSCacheDir(),
		}),
	}
	if force {
		// In force mode (auto_tls.mode=on), attempt certificate flow immediately
		// without waiting for reachability events. Useful for first bootstrap node.
		autoTLSOpts = append(autoTLSOpts, p2pforge.WithAllowPrivateForgeAddrs())
	}
	forgeAuth := strings.TrimSpace(cfg.AutoTLSForgeAuth())
	if forgeAuth != "" {
		autoTLSOpts = append(autoTLSOpts, p2pforge.WithForgeAuth(forgeAuth))
	}
	autoTLSMgr, err := p2pforge.NewP2PForgeCertMgr(autoTLSOpts...)
	if err != nil {
		return nil, err
	}
	port := cfg.AutoTLSPort()
	if port <= 0 {
		port = 443
	}
	*listenAddrs = append(*listenAddrs,
		fmt.Sprintf("/ip4/0.0.0.0/tcp/%d/tls/sni/*.%s/ws", port, p2pforge.DefaultForgeDomain),
		fmt.Sprintf("/ip6/::/tcp/%d/tls/sni/*.%s/ws", port, p2pforge.DefaultForgeDomain),
	)
	*wsOptions = append(*wsOptions, websocket.WithTLSConfig(autoTLSMgr.TLSConfig()))
	logging.Log("NODE", "autotls_enabled", map[string]string{
		"forge_domain": p2pforge.DefaultForgeDomain,
		"mode":         cfg.AutoTLSMode(),
		"port":         fmt.Sprintf("%d", port),
	})
	return autoTLSMgr, nil
}

func shouldEnablePublicService(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "public":
		return true
	case "private":
		return false
	default:
		if hasPublicIPv4() {
			logging.Log("NODE", "network_mode_auto", map[string]string{
				"decision": "public",
			})
			return true
		}
		logging.Log("NODE", "network_mode_auto", map[string]string{
			"decision": "private",
		})
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
		logging.Log("NODE", "reachability_subscribe_failed", map[string]string{
			"reason": err.Error(),
		})
		return
	}

	go func() {
		defer sub.Close()
		for evt := range sub.Out() {
			ev, ok := evt.(libp2pevent.EvtLocalReachabilityChanged)
			if !ok {
				continue
			}
			logging.Log("NODE", "autonat_reachability", map[string]string{
				"reachability": ev.Reachability.String(),
			})
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
		_, err := exec.Command("sysctl", "-w", kv).CombinedOutput()
		if err != nil {
			logging.Log("NODE", "quic_udp_tune_failed", map[string]string{
				"setting": kv,
				"reason":  err.Error(),
			})
		}
	}
}

func (n *Node) Close() error {
	n.closeOnce.Do(func() {
		if n.autoTLSMgr != nil {
			n.autoTLSMgr.Stop()
		}
		n.closeErr = n.Host.Close()
	})
	return n.closeErr
}

func (n *Node) SetStatusProvider(provider StatusProvider) {
	n.statusMu.Lock()
	n.status = provider
	n.statusMu.Unlock()
}

func (n *Node) SetMembershipManager(manager *membership.Manager) {
	n.memberMu.Lock()
	n.membership = manager
	n.memberMu.Unlock()
	n.evaluateRuntimeState("membership-set")
}

func (n *Node) SetMembershipAppliedHandler(fn func(snapshot membership.Snapshot)) {
	n.memberMu.Lock()
	n.onMembershipApplied = fn
	n.memberMu.Unlock()
}

func (n *Node) SetAdminProof(proof *membership.AdminProof) {
	n.memberMu.Lock()
	n.adminProof = proof
	n.memberMu.Unlock()
}

func (n *Node) isMember(peerID string) bool {
	n.memberMu.RLock()
	manager := n.membership
	n.memberMu.RUnlock()
	if manager == nil {
		return false
	}
	return manager.IsMember(peerID)
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

	logging.Log("NODE", "local_addrs", map[string]string{
		"addrs": fmt.Sprintf("%v", addrs),
	})
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
		if n.canUseBusinessProtocols() && len(n.Host.Network().Peers()) > 0 {
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
			// Keep retry loop in unconfigured mode to continue membership bootstrap attempts.
			return !n.canUseBusinessProtocols()
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
				logging.Log("NODE", "shutdown", map[string]string{
					"reason": shutdown.Reason,
				})
				if err := n.Close(); err != nil {
					logging.Log("NODE", "shutdown_failed", map[string]string{
						"reason": err.Error(),
					})
				}
				return
			}
		}
	}()
}

func (n *Node) registerConnectionNotifications() {
	n.Host.Network().Notify(&libp2pnet.NotifyBundle{
		ConnectedF: func(_ libp2pnet.Network, conn libp2pnet.Conn) {
			n.heartbeatUnsupported.Delete(conn.RemotePeer())
			n.statusUnsupported.Delete(conn.RemotePeer())
			if !n.allowPeer(conn.RemotePeer().String()) {
				logging.Log("NODE", "reject_peer", map[string]string{
					"peer_id": conn.RemotePeer().String(),
					"state":   string(n.RuntimeState()),
				})
				_ = conn.Close()
				return
			}
			n.Tracker.Upsert(remoteAddrInfo(conn.RemotePeer(), conn.RemoteMultiaddr()))
			if n.bus != nil {
				n.bus.Publish(events.PeerConnected{
					PeerID:     conn.RemotePeer().String(),
					RemoteAddr: conn.RemoteMultiaddr().String(),
					At:         time.Now().UTC(),
				})
			}
			n.evaluateRuntimeState("peer-connected")
		},
		DisconnectedF: func(network libp2pnet.Network, conn libp2pnet.Conn) {
			if len(network.ConnsToPeer(conn.RemotePeer())) == 0 {
				n.Tracker.Remove(conn.RemotePeer())
			}
			if !n.allowPeer(conn.RemotePeer().String()) {
				n.evaluateRuntimeState("peer-disconnected-non-member")
				return
			}
			if n.bus != nil {
				n.bus.Publish(events.PeerDisconnected{
					PeerID:     conn.RemotePeer().String(),
					RemoteAddr: conn.RemoteMultiaddr().String(),
					At:         time.Now().UTC(),
				})
			}
			n.evaluateRuntimeState("peer-disconnected")
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
