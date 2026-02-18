package config

import (
	"encoding/json"
	"fmt"
	"os"
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
