package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"p2pos/internal/events"
	"p2pos/internal/logging"
	"p2pos/internal/membership"

	"github.com/libp2p/go-libp2p/core/crypto"
)

type Config struct {
	InitConnections []Connection  `json:"init_connections"`
	Listen          ListenConfig  `json:"listen"`
	NetworkMode     string        `json:"network_mode"`
	AutoTLS         AutoTLSConfig `json:"auto_tls"`
	UpdateFeedURL   string        `json:"update_feed_url"`
	NodePrivateKey  string        `json:"node_private_key"`
	ClusterID       string        `json:"cluster_id"`
	SystemPubKey    string        `json:"system_pubkey"`
	AdminProof      AdminProof    `json:"admin_proof"`
	Members         []string      `json:"members"`
}

type AutoTLSConfig struct {
	Enabled   bool   `json:"enabled"`
	UserEmail string `json:"user_email"`
	CacheDir  string `json:"cache_dir"`
	ForgeAuth string `json:"forge_auth"`
}

type AdminProof struct {
	ClusterID string `json:"cluster_id"`
	PeerID    string `json:"peer_id"`
	Role      string `json:"role"`
	ValidFrom string `json:"valid_from"`
	ValidTo   string `json:"valid_to"`
	Sig       string `json:"sig"`
}

type Connection struct {
	Type    string `json:"type"`
	Address string `json:"address"`
}

type ListenConfig []string

func (l *ListenConfig) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*l = []string{one}
		return nil
	}

	var many []string
	if err := json.Unmarshal(data, &many); err == nil {
		*l = many
		return nil
	}

	return fmt.Errorf("listen must be a string or string array")
}

func (l ListenConfig) Values() []string {
	if len(l) == 0 {
		return []string{"0.0.0.0:4100", "[::]:4100"}
	}
	return []string(l)
}

type Store struct {
	mu          sync.RWMutex
	path        string
	bus         *events.Bus
	cfg         Config
	nodePrivKey crypto.PrivKey
}

const defaultConfigPath = "config.json"
const defaultUpdateFeedURL = "https://api.github.com/repos/ZhongWwwHhh/Ops-System/releases/latest"
const defaultNetworkMode = "auto"
const defaultClusterID = "default"
const defaultAutoTLSCacheDir = ".autotls-cache"

func NewStore(bus *events.Bus) *Store {
	return &Store{
		path: defaultConfigPath,
		bus:  bus,
		cfg:  Default(),
	}
}

func Default() Config {
	return Config{
		Listen:        ListenConfig{"0.0.0.0:4100", "[::]:4100"},
		NetworkMode:   defaultNetworkMode,
		AutoTLS:       AutoTLSConfig{Enabled: false, CacheDir: defaultAutoTLSCacheDir},
		UpdateFeedURL: defaultUpdateFeedURL,
		ClusterID:     defaultClusterID,
	}
}

func (s *Store) Init() error {
	cfg, err := Load(s.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}

		cfgVal := Default()
		if err := saveToFile(s.path, cfgVal); err != nil {
			return err
		}
		cfg = &cfgVal
	}

	normalized := normalize(*cfg)

	nodePrivKey, normalized, err := loadOrCreatePrivateKey(normalized, s.path)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.cfg = normalized
	s.nodePrivKey = nodePrivKey
	s.mu.Unlock()

	return nil
}

func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return copyConfig(s.cfg)
}

func (s *Store) ListenAddresses() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.cfg.Listen.Values()...)
}

func (s *Store) NodePrivateKey() crypto.PrivKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nodePrivKey
}

func (s *Store) NetworkMode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.NetworkMode
}

func (s *Store) AutoTLSEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.AutoTLS.Enabled
}

func (s *Store) AutoTLSUserEmail() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.AutoTLS.UserEmail
}

func (s *Store) AutoTLSCacheDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.AutoTLS.CacheDir
}

func (s *Store) AutoTLSForgeAuth() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.AutoTLS.ForgeAuth
}

func (s *Store) AdminProof() (*membership.AdminProof, bool, error) {
	s.mu.RLock()
	raw := s.cfg.AdminProof
	clusterID := s.cfg.ClusterID
	s.mu.RUnlock()
	return parseAdminProof(raw, clusterID)
}

func (s *Store) Update(next Config) error {
	normalized := normalize(next)

	if err := saveToFile(s.path, normalized); err != nil {
		return err
	}

	s.mu.Lock()
	s.cfg = normalized
	s.mu.Unlock()

	if s.bus != nil {
		s.bus.Publish(events.ConfigChanged{
			Listen:          append([]string(nil), normalized.Listen.Values()...),
			InitConnections: toEventConnections(normalized.InitConnections),
			NetworkMode:     normalized.NetworkMode,
			UpdateFeedURL:   normalized.UpdateFeedURL,
			At:              time.Now().UTC(),
		})
	}

	return nil
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func saveToFile(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

func normalize(cfg Config) Config {
	if len(cfg.Listen) == 0 {
		cfg.Listen = Default().Listen
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.NetworkMode))
	switch mode {
	case "", "auto":
		cfg.NetworkMode = defaultNetworkMode
	case "public", "private":
		cfg.NetworkMode = mode
	default:
		cfg.NetworkMode = defaultNetworkMode
	}
	if strings.TrimSpace(cfg.UpdateFeedURL) == "" {
		cfg.UpdateFeedURL = defaultUpdateFeedURL
	}
	cfg.NodePrivateKey = strings.TrimSpace(cfg.NodePrivateKey)
	cfg.SystemPubKey = strings.TrimSpace(cfg.SystemPubKey)
	cfg.ClusterID = strings.TrimSpace(cfg.ClusterID)
	if cfg.ClusterID == "" {
		cfg.ClusterID = defaultClusterID
	}
	cfg.AutoTLS.UserEmail = strings.TrimSpace(cfg.AutoTLS.UserEmail)
	cfg.AutoTLS.CacheDir = strings.TrimSpace(cfg.AutoTLS.CacheDir)
	cfg.AutoTLS.ForgeAuth = strings.TrimSpace(cfg.AutoTLS.ForgeAuth)
	if cfg.AutoTLS.CacheDir == "" {
		cfg.AutoTLS.CacheDir = defaultAutoTLSCacheDir
	}
	cfg.Members = dedupeTrimmed(cfg.Members)
	return cfg
}

func copyConfig(cfg Config) Config {
	next := Config{
		InitConnections: make([]Connection, len(cfg.InitConnections)),
		Listen:          append(ListenConfig(nil), cfg.Listen...),
		NetworkMode:     cfg.NetworkMode,
		AutoTLS:         cfg.AutoTLS,
		UpdateFeedURL:   cfg.UpdateFeedURL,
		NodePrivateKey:  cfg.NodePrivateKey,
		ClusterID:       cfg.ClusterID,
		SystemPubKey:    cfg.SystemPubKey,
		AdminProof:      cfg.AdminProof,
		Members:         append([]string(nil), cfg.Members...),
	}
	copy(next.InitConnections, cfg.InitConnections)
	return next
}

func dedupeTrimmed(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		id := strings.TrimSpace(v)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func (s *Store) UpdateFeedURL() (string, error) {
	s.mu.RLock()
	raw := s.cfg.UpdateFeedURL
	s.mu.RUnlock()
	return validateFeedURL(raw)
}

func validateFeedURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid update_feed_url %q, expected full URL", raw)
	}
	return value, nil
}

func toEventConnections(conns []Connection) []events.ConfigConnection {
	out := make([]events.ConfigConnection, 0, len(conns))
	for _, conn := range conns {
		out = append(out, events.ConfigConnection{
			Type:    conn.Type,
			Address: conn.Address,
		})
	}
	return out
}

func parseAdminProof(raw AdminProof, clusterID string) (*membership.AdminProof, bool, error) {
	isEmpty := strings.TrimSpace(raw.PeerID) == "" &&
		strings.TrimSpace(raw.Sig) == "" &&
		strings.TrimSpace(raw.ValidFrom) == "" &&
		strings.TrimSpace(raw.ValidTo) == "" &&
		strings.TrimSpace(raw.ClusterID) == "" &&
		strings.TrimSpace(raw.Role) == ""
	if isEmpty {
		return nil, false, nil
	}

	if strings.TrimSpace(raw.ClusterID) == "" {
		raw.ClusterID = clusterID
	}
	if strings.TrimSpace(raw.Role) == "" {
		raw.Role = "admin"
	}

	validFrom, err := parseTime(raw.ValidFrom)
	if err != nil {
		return nil, false, fmt.Errorf("admin_proof valid_from invalid: %w", err)
	}
	validTo, err := parseTime(raw.ValidTo)
	if err != nil {
		return nil, false, fmt.Errorf("admin_proof valid_to invalid: %w", err)
	}
	if raw.PeerID == "" || raw.Sig == "" {
		return nil, false, fmt.Errorf("admin_proof missing peer_id or sig")
	}

	return &membership.AdminProof{
		ClusterID: raw.ClusterID,
		PeerID:    raw.PeerID,
		Role:      raw.Role,
		ValidFrom: validFrom,
		ValidTo:   validTo,
		Sig:       raw.Sig,
	}, true, nil
}

func parseTime(raw string) (time.Time, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	if ts, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return ts.UTC(), nil
	}
	ts, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, err
	}
	return ts.UTC(), nil
}

func loadOrCreatePrivateKey(cfg Config, path string) (crypto.PrivKey, Config, error) {
	generateAndPersistNodeKey := func(reason string) (crypto.PrivKey, Config, error) {
		generatedKey, _, err := crypto.GenerateEd25519Key(nil)
		if err != nil {
			return nil, cfg, err
		}

		privKeyBytes, err := crypto.MarshalPrivateKey(generatedKey)
		if err != nil {
			return nil, cfg, err
		}

		encodedPrivKey := base64.StdEncoding.EncodeToString(privKeyBytes)
		cfg.NodePrivateKey = encodedPrivKey
		if err := saveToFile(path, cfg); err != nil {
			return nil, cfg, err
		}

		logging.Log("CONFIG", "node_key_generated", map[string]string{
			"reason": reason,
		})
		return generatedKey, cfg, nil
	}

	if cfg.NodePrivateKey == "" {
		return generateAndPersistNodeKey("missing")
	}

	privKeyBytes, err := base64.StdEncoding.DecodeString(cfg.NodePrivateKey)
	if err != nil {
		logging.Log("CONFIG", "node_key_regenerate", map[string]string{
			"reason": "invalid_base64",
		})
		return generateAndPersistNodeKey("invalid_base64")
	}

	loadedKey, err := crypto.UnmarshalPrivateKey(privKeyBytes)
	if err != nil {
		logging.Log("CONFIG", "node_key_regenerate", map[string]string{
			"reason": "invalid_key",
		})
		return generateAndPersistNodeKey("invalid_key")
	}

	logging.Log("CONFIG", "node_key_loaded", nil)
	return loadedKey, cfg, nil
}
