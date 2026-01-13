package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Long:  `Display the version of QWS.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("QWS version %s\n", version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
