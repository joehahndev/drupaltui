// Package config loads and saves the drupaltui configuration file.
//
// The config file holds the site URL, OAuth client ID, and chosen text
// format. The client secret is never stored here — it comes from the
// DRUPALTUI_CLIENT_SECRET environment variable or an interactive prompt,
// and lives only in memory.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const DefaultPageLimit = 50

type Config struct {
	BaseURL    string `yaml:"base_url"`
	ClientID   string `yaml:"client_id"`
	TextFormat string `yaml:"text_format"`
	PageLimit  int    `yaml:"page_limit,omitempty"`
}

// DefaultPath returns the platform config file path, e.g.
// %APPDATA%\drupaltui\config.yml on Windows or ~/.config/drupaltui/config.yml.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating config directory: %w", err)
	}
	return filepath.Join(dir, "drupaltui", "config.yml"), nil
}

// Load reads the config at path. A missing file is not an error: it returns
// an empty config so the first-run flow can fill it in.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{PageLimit: DefaultPageLimit}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	cfg.Normalize()
	return &cfg, nil
}

// Save writes the config to path, creating parent directories as needed.
func (c *Config) Save(path string) error {
	c.Normalize()
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing config %s: %w", path, err)
	}
	return nil
}

// Normalize trims the base URL and applies defaults.
func (c *Config) Normalize() {
	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	c.ClientID = strings.TrimSpace(c.ClientID)
	c.TextFormat = strings.TrimSpace(c.TextFormat)
	if c.PageLimit <= 0 {
		c.PageLimit = DefaultPageLimit
	}
}
