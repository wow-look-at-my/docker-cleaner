package cmd

import (
	"strings"

	"github.com/spf13/cobra"
)

// HelpDump renders every command's help. docs/cmdline_args.txt is exactly this
// text, and a test fails when the committed copy no longer matches.
func HelpDump() (string, error) {
	root := newRootCmd()
	// Cobra adds these during Execute, so Help() alone would omit them.
	root.InitDefaultHelpFlag()
	root.InitDefaultVersionFlag()

	var b strings.Builder
	for _, c := range append([]*cobra.Command{root}, root.Commands()...) {
		if c.Name() == "help" {
			continue
		}
		c.InitDefaultHelpFlag()
		c.SetOut(&b)
		defer c.SetOut(nil)
		if err := c.Help(); err != nil {
			return "", err
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}
