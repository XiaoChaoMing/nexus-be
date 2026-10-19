package main

import (
	"log"
	"os"
	_ "time/tzdata" // the scratch image has no zoneinfo; Studio summaries take IANA zones, reconcile-daily uses Asia/Ho_Chi_Minh

	"github.com/minhnn/nexuslive-be/services/billing/config"
	"github.com/minhnn/nexuslive-be/services/billing/internal/app"
)

func main() {
	cfg, err := config.NewConfig()
	if err != nil {
		log.Fatalf("Config error: %s", err)
	}

	// `billing <command>` runs a maintenance command (TSD §6.10); no argument starts the service.
	if len(os.Args) > 1 {
		os.Exit(app.RunCLI(cfg, os.Args[1:]))
	}

	app.Run(cfg)
}
