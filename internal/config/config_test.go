package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDefaultShadowKeysIncludeDelete(t *testing.T) {
	cfg := &Config{}
	cfg.setDefaults()

	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}

	if !strings.Contains(string(data), "delete: M-d") {
		t.Fatalf("default config should include delete key M-d, got:\n%s", string(data))
	}
}
