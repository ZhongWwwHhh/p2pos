package main

import (
	"fmt"
	"os"

	"p2pos/internal/app"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "keygen" {
		if err := app.RunKeygen(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "keygen failed:", err)
			os.Exit(1)
		}
		return
	}

	if err := app.Run(os.Args[1:]); err != nil {
		panic(fmt.Errorf("app startup failed: %w", err))
	}
}
