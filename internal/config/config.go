package config

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

const ConnectionTimeout = 30
const SqliteDatabasePath = "database.db"
const InitConfigPath = "./init_config.json"
const LogPath = "p2pos.log"
const LogMaxSize = 10 // MB

type InitConnection struct {
	Type    string `json:"type"`
	Address string `json:"address"`
}

type InitConfig struct {
	InitConnections []InitConnection `json:"init_connections"`
	SystemPubKey    string           `json:"system_public_key"`
	ListenAddr      string           `json:"listen_address"`
}

func isValidEd25519PubKey(pubkey string) bool {
	// 支持 base64 或 hex 编码
	if decoded, err := base64.StdEncoding.DecodeString(pubkey); err == nil && len(decoded) == 32 {
		return true
	}
	if decoded, err := hex.DecodeString(pubkey); err == nil && len(decoded) == 32 {
		return true
	}
	return false
}

func isValidDNS(addr string) bool {
	dnsName := "_dnsaddr." + addr
	txts, err := net.LookupTXT(dnsName)
	if err != nil || len(txts) == 0 {
		fmt.Printf("DNS lookup failed for %s: %v", dnsName, err)
		return false
	}

	fmt.Println("DNS TXT records:", txts)

	for _, txt := range txts {
		txt = strings.TrimPrefix(txt, "dnsaddr=")
		if isValidPeerMultiAddr(txt) {
			return true
		}
	}
	return false
}

func isValidPeerMultiAddr(addr string) bool {
	maddr, err := multiaddr.NewMultiaddr(addr)
	if err != nil {
		return false
	}
	peerID, err := maddr.ValueForProtocol(multiaddr.P_P2P)
	if err != nil || peerID == "" {
		return false
	}
	_, err = peer.Decode(peerID)
	return err == nil
}

// LoadInit 检查并加载 init_config.json 文件
func LoadInit(paths ...string) (*InitConfig, error) {
	var configPath string
	if len(paths) > 0 && paths[0] != "" {
		configPath = paths[0]
	} else {
		configPath = InitConfigPath
	}

	// 打开文件
	file, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open init_config.json: %w", err)
	}
	defer file.Close()

	// 解析 JSON
	var cfg InitConfig
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("failed to decode init_config.json: %w", err)
	}

	// 校验 system_public_key
	if len(cfg.SystemPubKey) == 0 {
		return nil, fmt.Errorf("system_public_key is required")
	}
	if !isValidEd25519PubKey(cfg.SystemPubKey) {
		return nil, fmt.Errorf("invalid ed25519 public key: %s", cfg.SystemPubKey)
	}

	// 校验 init_connections
	for _, conn := range cfg.InitConnections {
		if conn.Type != "dns" && conn.Type != "addr" {
			return nil, fmt.Errorf("invalid connection type: %s", conn.Type)
		}
		if conn.Type == "dns" && !isValidDNS(conn.Address) {
			return nil, fmt.Errorf("invalid dns address: %s", conn.Address)
		}
		if conn.Type == "addr" && !isValidPeerMultiAddr(conn.Address) {
			return nil, fmt.Errorf("invalid libp2p peer id: %s", conn.Address)
		}
	}

	// 返回解析后的配置
	return &cfg, nil
}
