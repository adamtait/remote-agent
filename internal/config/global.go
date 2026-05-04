// ra/internal/config/global.go
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adamtait/remote-agent/internal/logging"
	"gopkg.in/yaml.v3"
)

// UserConfigPath returns the default path for the user-wide config.
func UserConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("unable to determine home directory: %w", err)
	}
	return filepath.Join(home, ".config", "ra", "config.yaml"), nil
}

// LoadUserConfig reads and parses the user config file. Returns an error if
// the file is missing — user-level config is the required floor for every
// command. The returned error tells the user how to recover.
func LoadUserConfig(path string) (*UserConfig, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no %s found; run 'ra setup' to create one", path)
	}
	if err != nil {
		return nil, err
	}
	var cfg UserConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	logging.L().Debug("config loaded", "path", path, "type", "user")
	return &cfg, nil
}

// LoadProjectConfig reads and parses a .ra/config.yaml file. An empty path
// signals "no project config in scope" and yields an empty ProjectConfig so
// callers can merge unconditionally.
func LoadProjectConfig(path string) (*ProjectConfig, error) {
	if path == "" {
		return &ProjectConfig{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg ProjectConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
