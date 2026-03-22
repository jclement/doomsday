package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/jclement/doomsday/internal/whimsy"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Long:  `Display the doomsday version, build commit, build date, and a witty tagline.`,
	RunE:  runVersion,
}

func runVersion(cmd *cobra.Command, args []string) error {
	if flagJSON {
		out := map[string]string{
			"version": version,
			"commit":  commit,
			"date":    date,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	fmt.Printf("%s %s %s built %s\n",
		cliStyles.Brand.Render("doomsday"),
		version,
		cliStyles.Muted.Render("("+commit+")"),
		date,
	)

	if !flagNoWhimsy && !flagQuiet {
		if tagline := whimsy.VersionTagline(); tagline != "" {
			fmt.Println(cliStyles.Tagline.Render(tagline))
		}
	}

	return nil
}
