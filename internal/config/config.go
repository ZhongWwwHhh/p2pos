package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"p2pos/internal/database"
	"strings"
	"sync"
	"time"

	"p2pos/internal/events"

	"github.com/libp2p/go-libp2p/core/crypto"
)

type Config struct {
	InitConnections []Connection `json:"init_connections"`
	Listen          ListenConfig `json:"listen"`
	NetworkMode     string       `json:"network_mode"`
	UpdateFeedURL   string       `json:"update_feed_url"`
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
		UpdateFeedURL: defaultUpdateFeedURL,
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

	nodePrivKey, err := loadOrCreatePrivateKey()
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
	return cfg
}

func copyConfig(cfg Config) Config {
	next := Config{
		InitConnections: make([]Connection, len(cfg.InitConnections)),
		Listen:          append(ListenConfig(nil), cfg.Listen...),
		NetworkMode:     cfg.NetworkMode,
		UpdateFeedURL:   cfg.UpdateFeedURL,
	}
	copy(next.InitConnections, cfg.InitConnections)
	return next
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

func loadOrCreatePrivateKey() (crypto.PrivKey, error) {
	storedPrivKey, err := database.LoadNodePrivateKey()
	if err != nil {
		return nil, err
	}

	generateAndPersistNodeKey := func() (crypto.PrivKey, error) {
		generatedKey, _, err := crypto.GenerateEd25519Key(nil)
		if err != nil {
			return nil, err
		}

		privKeyBytes, err := crypto.MarshalPrivateKey(generatedKey)
		if err != nil {
			return nil, err
		}

		encodedPrivKey := base64.StdEncoding.EncodeToString(privKeyBytes)
		if err := database.SaveNodePrivateKey(encodedPrivKey); err != nil {
			return nil, err
		}

		fmt.Println("[NODE] Generated and persisted new node private key")
		return generatedKey, nil
	}

	if storedPrivKey == "" {
		return generateAndPersistNodeKey()
	}

	privKeyBytes, err := base64.StdEncoding.DecodeString(storedPrivKey)
	if err != nil {
		fmt.Printf("[NODE] Stored private key is invalid base64, regenerating: %v\n", err)
		return generateAndPersistNodeKey()
	}

	loadedKey, err := crypto.UnmarshalPrivateKey(privKeyBytes)
	if err != nil {
		fmt.Printf("[NODE] Stored private key is invalid, regenerating: %v\n", err)
		return generateAndPersistNodeKey()
	}

	fmt.Println("[NODE] Loaded persisted node private key")
	return loadedKey, nil
}
