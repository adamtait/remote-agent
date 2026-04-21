// ws/internal/config/global.go
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// GlobalConfigPath returns the default path for the global user config.
func GlobalConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("unable to determine home directory: %w", err)
	}
	return filepath.Join(home, ".config", "ws.yaml"), nil
}

// LoadGlobalConfig reads and parses the global config file.
// Returns an empty GlobalConfig (no error) if the file does not exist.
func LoadGlobalConfig(path string) (*GlobalConfig, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &GlobalConfig{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg GlobalConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
