package picker

import (
	"bytes"
	"strings"
	"testing"
)

func TestEncodeItemsUsesNULRecordsAndOpaqueKeys(t *testing.T) {
	items := []Item{
		{Key: "first", Label: "app:feat/auth  /tmp/one"},
		{Key: "second", Label: "app:fix/line\nbreak  /tmp/two"},
	}

	encoded := encodeItems(items)
	if bytes.Count(encoded, []byte{0}) != 2 {
		t.Fatalf("NUL count = %d, want 2", bytes.Count(encoded, []byte{0}))
	}
	if !strings.Contains(string(encoded), "0\tapp:feat/auth") || !strings.Contains(string(encoded), "1\tapp:fix/line break") {
		t.Fatalf("encoded = %q", encoded)
	}
}

func TestDecodeSelectionUsesIndexInsteadOfDisplayText(t *testing.T) {
	items := []Item{{Key: "path-with\nnewline", Label: "display"}, {Key: "second", Label: "other"}}

	got, err := decodeSelection([]byte("1\tchanged display\x00"), items)
	if err != nil {
		t.Fatalf("decodeSelection() error = %v", err)
	}
	if got != "second" {
		t.Fatalf("decodeSelection() = %q, want second", got)
	}
}

func TestDecodeSelectionsReturnsEveryFZFSelection(t *testing.T) {
	items := []Item{{Key: "first", Label: "one"}, {Key: "second", Label: "two"}, {Key: "third", Label: "three"}}

	got, err := decodeSelections([]byte("2\tthree\x000\tone\x00"), items)
	if err != nil {
		t.Fatalf("decodeSelections() error = %v", err)
	}
	if strings.Join(got, ",") != "third,first" {
		t.Fatalf("decodeSelections() = %#v", got)
	}
}
