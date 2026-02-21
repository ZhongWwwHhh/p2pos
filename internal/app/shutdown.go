package app

import (
	"time"

	"p2pos/internal/events"
)

type BusShutdownRequester struct {
	bus *events.Bus
}

func NewBusShutdownRequester(bus *events.Bus) *BusShutdownRequester {
	return &BusShutdownRequester{bus: bus}
}

func (r *BusShutdownRequester) RequestShutdown(reason string) {
	if r == nil || r.bus == nil {
		return
	}
	r.bus.Publish(events.ShutdownRequested{
		Reason: reason,
		At:     time.Now().UTC(),
	})
}
