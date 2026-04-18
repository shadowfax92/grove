package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDefaultShadowConfigIncludesShadowKeysAndCenteredMaximize(t *testing.T) {
	cfg := &Config{}
	cfg.setDefaults()

	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}

	if !strings.Contains(string(data), "delete: M-d") {
		t.Fatalf("default config should include delete key M-d, got:\n%s", string(data))
	}
	if !strings.Contains(string(data), "maximize: C-S-Y") {
		t.Fatalf("default config should include maximize key C-S-Y, got:\n%s", string(data))
	}
	if !strings.Contains(string(data), "max_width: 50%") {
		t.Fatalf("default config should include centered max width 50%%, got:\n%s", string(data))
	}
}
