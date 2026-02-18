package tasks

import (
	"context"
	"fmt"
	"time"

	"p2pos/internal/network"

	peerstore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/ping"
)

type PingTask struct {
	tracker     *network.Tracker
	pingService *ping.PingService
}

func NewPingTask(tracker *network.Tracker, pingService *ping.PingService) *PingTask {
	return &PingTask{
		tracker:     tracker,
		pingService: pingService,
	}
}

func (t *PingTask) Name() string {
	return "ping-peers"
}

func (t *PingTask) Interval() time.Duration {
	return 10 * time.Second
}

func (t *PingTask) RunOnStart() bool {
	return false
}

func (t *PingTask) Run(ctx context.Context) error {
	peers := t.tracker.GetAll()
	if len(peers) == 0 {
		return nil
	}

	for _, p := range peers {
		go func(peerID peerstore.ID) {
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			ch := t.pingService.Ping(pingCtx, peerID)
			select {
			case <-pingCtx.Done():
				fmt.Printf("[PING] Ping timeout %s: %v\n", peerID.String(), pingCtx.Err())
			case res := <-ch:
				fmt.Printf("[PING] Pinged %s: RTT %v\n", peerID.String(), res.RTT)
			}
		}(p.ID)
	}

	return nil
}
