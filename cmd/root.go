package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var Version = "dev"

var ErrCancelled = errors.New("")

var (
	helpHeaderColor = color.New(color.Bold, color.FgCyan)
	helpCmdColor    = color.New(color.FgHiGreen)
	helpAliasColor  = color.New(color.FgYellow)
	helpHintColor   = color.New(color.Faint)
)

func helpHeader(s string) string  { return helpHeaderColor.Sprint(s) }
func helpCmdCol(s string) string  { return helpCmdColor.Sprint(s) }
func helpHint(s string) string    { return helpHintColor.Sprint(s) }
func helpAliases(aliases []string) string {
	return helpAliasColor.Sprintf("(aliases: %s)", strings.Join(aliases, ", "))
}

var groupOrder = []string{
	"Workspaces:",
	"Setup:",
	"Other:",
}

func groupedHelp(cmd *cobra.Command) string {
	groups := map[string][]*cobra.Command{}
	for _, c := range cmd.Commands() {
		if !c.IsAvailableCommand() && c.Name() != "help" {
			continue
		}
		g := c.Annotations["group"]
		if g == "" {
			g = "Other:"
		}
		groups[g] = append(groups[g], c)
	}

	var b strings.Builder
	for _, name := range groupOrder {
		cmds, ok := groups[name]
		if !ok {
			continue
		}
		b.WriteString("\n" + helpHeader(name) + "\n")
		for _, c := range cmds {
			line := "  " + helpCmdCol(fmt.Sprintf("%-12s", c.Name())) + " " + c.Short
			if len(c.Aliases) > 0 {
				line += " " + helpAliases(c.Aliases)
			}
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

const usageTemplate = `{{helpHeader "Usage:"}}{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

{{helpHeader "Aliases:"}}
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

{{helpHeader "Examples:"}}
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}
{{groupedHelp .}}{{end}}{{if .HasAvailableLocalFlags}}

{{helpHeader "Flags:"}}
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

{{helpHeader "Global Flags:"}}
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

{{helpHint (printf "Use \"%s [command] --help\" for more information." .CommandPath)}}{{end}}
`

var rootCmd = &cobra.Command{
	Use:           "grove",
	Short:         "Tmux workspaces powered by git worktrees",
	Version:       Version,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return listCmd.RunE(cmd, args)
	},
}

func init() {
	cobra.AddTemplateFunc("helpHeader", helpHeader)
	cobra.AddTemplateFunc("helpCmdCol", helpCmdCol)
	cobra.AddTemplateFunc("helpAliases", helpAliases)
	cobra.AddTemplateFunc("helpHint", helpHint)
	cobra.AddTemplateFunc("groupedHelp", groupedHelp)

	rootCmd.SetUsageTemplate(usageTemplate)
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		if errors.Is(err, ErrCancelled) {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
