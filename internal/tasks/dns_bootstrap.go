package tasks

import (
	"context"
	"fmt"
	"time"

	"p2pos/internal/discovery"
	"p2pos/internal/network"
	"p2pos/internal/scheduler"

	"github.com/libp2p/go-libp2p/core/host"
)

type DNSBootstrapTask struct {
	node       host.Host
	dnsAddress string
	tracker    *network.Tracker
}

func NewDNSBootstrapTask(node host.Host, dnsAddress string, tracker *network.Tracker) *DNSBootstrapTask {
	return &DNSBootstrapTask{
		node:       node,
		dnsAddress: dnsAddress,
		tracker:    tracker,
	}
}

func (t *DNSBootstrapTask) Name() string {
	return "dns-bootstrap:" + t.dnsAddress
}

func (t *DNSBootstrapTask) Interval() time.Duration {
	return 1 * time.Minute
}

func (t *DNSBootstrapTask) RunOnStart() bool {
	return true
}

func (t *DNSBootstrapTask) Run(ctx context.Context) error {
	if len(t.node.Network().Peers()) > 0 {
		fmt.Printf("[DNS] Existing peer connection detected, stopping DNS discovery for %s\n", t.dnsAddress)
		return scheduler.ErrTaskCompleted
	}

	result, discoveredPeer, multiAddrStr, err := discovery.DiscoverAndConnectDNS(ctx, t.node, t.dnsAddress)
	if err != nil {
		fmt.Printf("[DNS] %v\n", err)
		return nil
	}

	switch result {
	case discovery.DNSResultNoRecord:
		fmt.Printf("[DNS] No records found for %s\n", t.dnsAddress)
		return nil
	case discovery.DNSResultSelf:
		fmt.Printf("[DNS] Discovered address is ourselves, skipping\n")
		return nil
	case discovery.DNSResultConnected:
		fmt.Printf("[DNS] Connected to peer from %s: %s\n", t.dnsAddress, discoveredPeer.ID)
		t.tracker.Add(multiAddrStr, discoveredPeer)
		return scheduler.ErrTaskCompleted
	default:
		return nil
	}
}
