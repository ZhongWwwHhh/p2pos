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

type ConfigConnection struct {
	Type    string
	Address string
}

type ConfigChanged struct {
	Listen          []string
	InitConnections []ConfigConnection
	UpdateFeedURL   string
	At              time.Time
}
