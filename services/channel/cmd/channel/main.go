package main

import (
	"log"

	"github.com/minhnn/nexuslive-be/services/channel/config"
	"github.com/minhnn/nexuslive-be/services/channel/internal/app"
)

func main() {
	cfg, err := config.NewConfig()
	if err != nil {
		log.Fatalf("Config error: %s", err)
	}

	app.Run(cfg)
}
