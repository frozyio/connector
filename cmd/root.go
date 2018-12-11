package cmd

import (
	"fmt"
	"os"

	"gitlab.com/frozy.io/connector/app"
)

// Execute is a main entry
func Execute() {
	config := app.Config{
		Init: getInitConfig(),
	}

	fmt.Printf("Initialized with: %+v\n", config.Init)

	err := config.ExecuteConnector()
	if err != nil {
		fmt.Println("Fatal error: ", err.Error())
		os.Exit(1)
	}
}
