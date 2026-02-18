package network

import (
	"fmt"
	"net"
	"strings"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	peerstore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/ping"
)

type Node struct {
	Host        host.Host
	PingService *ping.PingService
	Tracker     *Tracker
}

func NewNode(listens []string, privKey crypto.PrivKey) (*Node, error) {
	listenAddrs, err := buildListenMultiaddrs(listens)
	if err != nil {
		return nil, err
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
	}
	return n, nil
}

func (n *Node) Close() error {
	return n.Host.Close()
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
