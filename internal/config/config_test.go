package config

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestIsValidEd25519PubKey(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)

	tests := []struct {
		name   string
		pubkey string
		valid  bool
	}{
		{"ValidBase64", base64.StdEncoding.EncodeToString(pub), true},
		{"ValidHex", hex.EncodeToString(pub), true},
		{"InvalidLength", "MCowBQYDK2VwAyEAAQIDBAUGBwgJCgsMDQ4P", false},
		{"InvalidBase64", "InvalidBase64String!!!", false},
		{"InvalidHex", "GHIJKLMNOPQRSTUVWXYZ123456", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing pubkey: %s", tt.pubkey)
			if got := isValidEd25519PubKey(tt.pubkey); got != tt.valid {
				t.Errorf("isValidEd25519PubKey() = %v, want %v", got, tt.valid)
			}
		})
	}
}

func TestIsValidPeerMultiAddr(t *testing.T) {
	// 生成一个合法的 PeerID
	peerID, _ := peer.Decode("12D3KooWJ6v8kQ1k6w8y8Q9k8k1k8k1k8k1k8k1k8k1k8k1k8k1k")
	validAddr := "/ip4/127.0.0.1/tcp/4001/p2p/" + peerID.String()
	invalidAddr := "/ip4/127.0.0.1/tcp/4001"
	invalidPeerAddr := "/ip4/127.0.0.1/tcp/4001/p2p/invalidpeerid"

	tests := []struct {
		name string
		addr string
		want bool
	}{
		{"ValidPeerMultiAddr", validAddr, true},
		{"NoP2P", invalidAddr, false},
		{"InvalidPeerID", invalidPeerAddr, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing address: %s", tt.addr)
			got := isValidPeerMultiAddr(tt.addr)
			if got != tt.want {
				t.Errorf("isValidPeerMultiAddr(%q) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestIsValidDNS(t *testing.T) {
	addr := "init.test.cnss.dev"
	if !isValidDNS(addr) {
		t.Errorf("isValidDNS(%q) = false, want true", addr)
	}
}

func TestLoadInit(t *testing.T) {
	cfg, err := LoadInit("../../example.init_config.json")
	t.Logf("Loaded InitConfig: %+v", cfg)
	if err != nil {
		t.Errorf("LoadInit() error: %v", err)
	}
}
