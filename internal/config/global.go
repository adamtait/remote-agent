// ra/internal/config/global.go
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

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

// LoadUserConfig reads and parses the user config file.
// Returns an empty UserConfig (no error) if the file does not exist —
// validation (not loading) is responsible for flagging missing required keys.
func LoadUserConfig(path string) (*UserConfig, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &UserConfig{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg UserConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// LoadProjectConfig reads and parses a .ra/config.yaml file.
func LoadProjectConfig(path string) (*ProjectConfig, error) {
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
