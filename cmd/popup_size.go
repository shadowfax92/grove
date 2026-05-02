package cmd

import (
	"strconv"

	"grove/internal/config"
	"grove/internal/tmux"
)

// resolvePopupSize chooses popup width/height based on the client's terminal
// size. When TargetCols/TargetRows are set, the popup is capped at those
// absolute column/row counts (centered by tmux). When the cap would land
// within 85% of the client size, the popup expands to 100% to avoid
// awkward sliver gaps.
func resolvePopupSize(cfg config.ShadowPopupConfig, client string) (width, height string) {
	if cfg.TargetCols <= 0 || cfg.TargetRows <= 0 {
		return cfg.Width, cfg.Height
	}

	cols, rows, err := tmux.ClientSize(client)
	if err != nil || cols <= 0 || rows <= 0 {
		return cfg.Width, cfg.Height
	}

	w := cfg.TargetCols
	if w > cols || float64(w) >= 0.85*float64(cols) {
		w = cols
	}

	h := cfg.TargetRows
	if h > rows || float64(h) >= 0.85*float64(rows) {
		h = rows
	}

	return strconv.Itoa(w), strconv.Itoa(h)
}
