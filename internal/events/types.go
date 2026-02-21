package events

import "time"

type PeerConnected struct {
	PeerID     string
	RemoteAddr string
	At         time.Time
}

type PeerDisconnected struct {
	PeerID     string
	RemoteAddr string
	At         time.Time
}

type PeerStateObserved struct {
	PeerID       string
	RemoteAddr   string
	LastSeenAt   time.Time
	Reachability string
	ObservedBy   string
	ObservedAt   time.Time
}

type PeerHeartbeat struct {
	PeerID     string
	RemoteAddr string
	At         time.Time
}

type ConfigConnection struct {
	Type    string
	Address string
}

type ConfigChanged struct {
	Listen          []string
	InitConnections []ConfigConnection
	NetworkMode     string
	UpdateFeedURL   string
	At              time.Time
}

type ShutdownRequested struct {
	Reason string
	At     time.Time
}
