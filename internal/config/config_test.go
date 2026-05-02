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
	if !strings.Contains(string(data), "maximize: M-y") {
		t.Fatalf("default config should include maximize key M-y, got:\n%s", string(data))
	}
	if !strings.Contains(string(data), "max_width: 100%") {
		t.Fatalf("default config should include full-screen max width 100%%, got:\n%s", string(data))
	}
	if !strings.Contains(string(data), "target_cols: 360") {
		t.Fatalf("default config should include target_cols 360, got:\n%s", string(data))
	}
	if !strings.Contains(string(data), "target_rows: 75") {
		t.Fatalf("default config should include target_rows 75, got:\n%s", string(data))
	}
}
