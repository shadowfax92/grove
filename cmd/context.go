package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"grove/internal/catalog"
	"grove/internal/config"
	"grove/internal/inventory"

	"github.com/spf13/cobra"
)

type commandContext struct {
	directory string
	config    *config.Config
	catalog   *catalog.Catalog
	inventory *inventory.Inventory
}

func (a *application) loadContext(cmd *cobra.Command) (*commandContext, error) {
	directory, err := a.workingDirectory()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	cat, warnings := catalog.Build(cfg, directory)
	for _, warning := range warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warning.Error())
	}
	inv, failures := inventory.Build(cat)
	for _, failure := range failures {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", failure.Error())
	}
	return &commandContext{directory: directory, config: cfg, catalog: cat, inventory: inv}, nil
}

func (a *application) workingDirectory() (string, error) {
	base, err := a.dependencies.getwd()
	if err != nil {
		return "", err
	}
	directory := a.directory
	if directory == "" {
		directory = base
	} else if !filepath.IsAbs(directory) {
		directory = filepath.Join(base, directory)
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(directory)
	if err != nil {
		return "", fmt.Errorf("directory %s: %w", directory, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("directory %s is not a directory", directory)
	}
	return directory, nil
}
