package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

type outputStyle struct {
	enabled bool
}

func (a *application) style(writer io.Writer) outputStyle {
	if a.jsonOutput || a.nullOutput {
		return outputStyle{}
	}
	switch a.colorMode {
	case "always":
		return outputStyle{enabled: true}
	case "never":
		return outputStyle{}
	}
	if _, disabled := os.LookupEnv("NO_COLOR"); disabled || os.Getenv("TERM") == "dumb" {
		return outputStyle{}
	}
	file, ok := writer.(*os.File)
	if !ok {
		return outputStyle{}
	}
	info, err := file.Stat()
	return outputStyle{enabled: err == nil && info.Mode()&os.ModeCharDevice != 0}
}

func (s outputStyle) apply(code, value string) string {
	if !s.enabled || value == "" {
		return value
	}
	return "\x1b[" + code + "m" + value + "\x1b[0m"
}

func (s outputStyle) heading(value string) string {
	return s.apply("1;36", value)
}

func (s outputStyle) branch(value string) string {
	return s.apply("32", value)
}

func (s outputStyle) info(value string) string {
	return s.apply("36", value)
}

func (s outputStyle) attention(value string) string {
	return s.apply("33", value)
}

func (s outputStyle) danger(value string) string {
	return s.apply("31", value)
}

func (s outputStyle) muted(value string) string {
	return s.apply("2", value)
}

func (a *application) writeHelp(command *cobra.Command, _ []string) {
	description := strings.TrimSpace(command.Long)
	if description == "" {
		description = strings.TrimSpace(command.Short)
	}
	var content strings.Builder
	if description != "" {
		content.WriteString(description)
		content.WriteString("\n\n")
	}
	content.WriteString(command.UsageString())
	if _, err := fmt.Fprint(command.OutOrStdout(), colorizeHelp(content.String(), a.style(command.OutOrStdout()))); err != nil {
		fmt.Fprintln(command.ErrOrStderr(), err)
	}
}

func colorizeHelp(content string, style outputStyle) string {
	if !style.enabled {
		return content
	}
	headings := map[string]bool{
		"Usage:":                  true,
		"Aliases:":                true,
		"Examples:":               true,
		"Available Commands:":     true,
		"Flags:":                  true,
		"Global Flags:":           true,
		"Additional help topics:": true,
	}
	section := ""
	lines := strings.SplitAfter(content, "\n")
	for index, line := range lines {
		ending := ""
		if strings.HasSuffix(line, "\n") {
			line = strings.TrimSuffix(line, "\n")
			ending = "\n"
		}
		trimmed := strings.TrimSpace(line)
		switch {
		case headings[trimmed]:
			section = trimmed
			line = style.heading(line)
		case trimmed == "":
			section = ""
		case section == "Available Commands:" && strings.HasPrefix(line, "  "):
			indent := len(line) - len(strings.TrimLeft(line, " "))
			rest := line[indent:]
			end := strings.IndexAny(rest, " \t")
			if end > 0 {
				line = line[:indent] + style.branch(rest[:end]) + rest[end:]
			}
		case strings.HasPrefix(trimmed, "Use \""):
			line = style.muted(line)
		}
		lines[index] = line + ending
	}
	return strings.Join(lines, "")
}
