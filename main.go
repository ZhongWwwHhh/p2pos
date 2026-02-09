package main

import (
	"os"
	"p2pos/internal/initializer"
	"p2pos/internal/logger"
)

func main() {
	logger.Init()
	logger.Info("Starting...", nil)

	if err := initializer.Init(); err != nil {
		os.Exit(1)
	}

	// if err := network.InitNetworkConnect(); err != nil {
	// 	os.Exit(1)
	// }
}
