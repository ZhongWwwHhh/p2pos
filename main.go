package main

import (
	"fmt"
	"os"

	"p2pos/internal/app"
)

func main() {
	if err := app.Run(os.Args[1:]); err != nil {
		panic(fmt.Errorf("app startup failed: %w", err))
	}
}
