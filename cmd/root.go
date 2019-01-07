package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"gitlab.com/frozy.io/connector/app"
)

type connectorArgs struct {
	configFile string
}

var params connectorArgs

var rootCmd = &cobra.Command{
	Use:   "connector",
	Short: "Frozy Connector",
	Run: func(cmd *cobra.Command, args []string) {
		if err := app.Execute(params.configFile); err != nil {
			fmt.Println("Fatal error:", err.Error())
			os.Exit(1)
		}
	},
}

func init() {
	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "display application version",
		Run: func(cmd *cobra.Command, args []string) {
			println(app.Version)
		},
	}
	rootCmd.AddCommand(versionCmd)
	rootCmd.PersistentFlags().StringVar(&params.configFile,
		"config", "", "config file (default is $HOME/.frozy-connector/connector.yaml)")
}

// Execute is a main entry
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println("Fatal error:", err.Error())
		os.Exit(1)
	}
}
