package events

import "sync"

type Bus struct {
	mu   sync.RWMutex
	subs map[chan any]struct{}
}

func NewBus() *Bus {
	return &Bus{
		subs: make(map[chan any]struct{}),
	}
}

func (b *Bus) Subscribe(buffer int) (<-chan any, func()) {
	if buffer <= 0 {
		buffer = 1
	}

	ch := make(chan any, buffer)

	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}

	return ch, cancel
}

func (b *Bus) Publish(evt any) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for ch := range b.subs {
		select {
		case ch <- evt:
		default:
		}
	}
}
