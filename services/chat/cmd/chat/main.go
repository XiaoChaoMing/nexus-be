package main

import (
	"log"
	"os"

	"github.com/minhnn/nexuslive-be/services/chat/config"
	"github.com/minhnn/nexuslive-be/services/chat/internal/app"
)

func main() {
	cfg, err := config.NewConfig()
	if err != nil {
		log.Fatalf("Config error: %s", err)
	}

	// chat dev-grant follow|sub|mod|vip|owner --channel <id> --user <id> (demo data, NXL_ENV=dev only)
	if len(os.Args) > 1 && os.Args[1] == "dev-grant" {
		if err = app.DevGrant(cfg, os.Args[2:], os.Stdout); err != nil {
			log.Fatalf("dev-grant: %s", err)
		}

		return
	}

	app.Run(cfg)
}
