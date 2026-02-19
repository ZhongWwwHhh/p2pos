package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"p2pos/internal/events"
)

type Config struct {
	InitConnections []Connection `json:"init_connections"`
	Listen          ListenConfig `json:"listen"`
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
	mu   sync.RWMutex
	path string
	bus  *events.Bus
	cfg  Config
}

const defaultConfigPath = "config.json"

func NewStore(bus *events.Bus) *Store {
	return &Store{
		path: defaultConfigPath,
		bus:  bus,
		cfg:  Default(),
	}
}

func Default() Config {
	return Config{
		Listen: ListenConfig{"0.0.0.0:4100", "[::]:4100"},
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

	s.mu.Lock()
	s.cfg = normalized
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
			At:              time.Now(),
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
	return cfg
}

func copyConfig(cfg Config) Config {
	next := Config{
		InitConnections: make([]Connection, len(cfg.InitConnections)),
		Listen:          append(ListenConfig(nil), cfg.Listen...),
	}
	copy(next.InitConnections, cfg.InitConnections)
	return next
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
