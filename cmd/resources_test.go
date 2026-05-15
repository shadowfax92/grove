package cmd

import (
	"reflect"
	"testing"

	"grove/internal/resources"
)

func TestParseResourceCleanupSelection(t *testing.T) {
	usages := []resources.WindowUsage{
		{Target: "g/app:0"},
		{Target: "gs/sh/2:0"},
		{Target: "g/app:1"},
	}

	got := parseResourceCleanupSelection(usages, "1\tgs/sh/2:0\t512 MB\n2\tg/app:1\t128 MB\n")

	want := []resources.WindowUsage{
		{Target: "gs/sh/2:0"},
		{Target: "g/app:1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseResourceCleanupSelection() = %#v, want %#v", got, want)
	}
}

func TestFormatResourceRSS(t *testing.T) {
	tests := []struct {
		name string
		kb   int64
		want string
	}{
		{name: "kilobytes", kb: 512, want: "512 KB"},
		{name: "megabytes", kb: 1536, want: "1.5 MB"},
		{name: "gigabytes", kb: 2 * 1024 * 1024, want: "2.0 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatResourceRSS(tt.kb); got != tt.want {
				t.Fatalf("formatResourceRSS(%d) = %q, want %q", tt.kb, got, tt.want)
			}
		})
	}
}
