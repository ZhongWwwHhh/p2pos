package network

import (
	"sync"

	peerstore "github.com/libp2p/go-libp2p/core/peer"
)

type Tracker struct {
	mu    sync.RWMutex
	peers map[peerstore.ID]peerstore.AddrInfo
}

func NewTracker() *Tracker {
	return &Tracker{
		peers: make(map[peerstore.ID]peerstore.AddrInfo),
	}
}

func (t *Tracker) Upsert(p peerstore.AddrInfo) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.peers[p.ID] = p
}

func (t *Tracker) Remove(peerID peerstore.ID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.peers, peerID)
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
