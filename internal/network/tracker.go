package network

import (
	"sync"

	peerstore "github.com/libp2p/go-libp2p/core/peer"
)

type Tracker struct {
	mu    sync.RWMutex
	peers map[string]peerstore.AddrInfo
}

func NewTracker() *Tracker {
	return &Tracker{
		peers: make(map[string]peerstore.AddrInfo),
	}
}

func (t *Tracker) Add(addr string, p peerstore.AddrInfo) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.peers[addr] = p
}

func (t *Tracker) GetAll() []peerstore.AddrInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make([]peerstore.AddrInfo, 0, len(t.peers))
	for _, p := range t.peers {
		result = append(result, p)
	}
	return result
}
