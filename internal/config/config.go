package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Shadow ShadowConfig `yaml:"shadow"`
}

type ShadowConfig struct {
	Popup ShadowPopupConfig `yaml:"popup"`
	Keys  ShadowKeys        `yaml:"keys"`
}

type ShadowPopupConfig struct {
	Width     string `yaml:"width"`
	Height    string `yaml:"height"`
	MaxWidth  string `yaml:"max_width"`
	MaxHeight string `yaml:"max_height"`
}

type ShadowKeys struct {
	Vim      string `yaml:"vim"`
	Shell    string `yaml:"shell"`
	Delete   string `yaml:"delete"`
	Maximize string `yaml:"maximize"`
}

func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "grove", "config.yaml"), nil
}

func Load() (*Config, error) {
	return load()
}

// LoadFast is kept for API compatibility with shadow commands.
func LoadFast() (*Config, error) {
	return load()
}

func load() (*Config, error) {
	path, err := DefaultConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return createDefault(path)
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	cfg.setDefaults()
	return &cfg, nil
}

func (c *Config) setDefaults() {
	if c.Shadow.Popup.Width == "" {
		c.Shadow.Popup.Width = "80%"
	}
	if c.Shadow.Popup.Height == "" {
		c.Shadow.Popup.Height = "85%"
	}
	if c.Shadow.Popup.MaxWidth == "" {
		c.Shadow.Popup.MaxWidth = "50%"
	}
	if c.Shadow.Popup.MaxHeight == "" {
		c.Shadow.Popup.MaxHeight = "100%"
	}
	if c.Shadow.Keys.Vim == "" {
		c.Shadow.Keys.Vim = "M-i"
	}
	if c.Shadow.Keys.Shell == "" {
		c.Shadow.Keys.Shell = "M-o"
	}
	if c.Shadow.Keys.Delete == "" {
		c.Shadow.Keys.Delete = "M-d"
	}
	if c.Shadow.Keys.Maximize == "" {
		c.Shadow.Keys.Maximize = "C-S-Y"
	}
}

func createDefault(path string) (*Config, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}

	cfg := &Config{}
	cfg.setDefaults()

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}

	header := "# Grove configuration\n# Popup shadow sessions for vim and shell\n\n"
	if err := os.WriteFile(path, []byte(header+string(data)), 0644); err != nil {
		return nil, err
	}

	return cfg, nil
}
