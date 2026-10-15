package main

import (
	"log"
	_ "time/tzdata" // users' time zones in a scratch image (daily e-mail cap, times in e-mails)

	"github.com/minhnn/nexuslive-be/services/notification/config"
	"github.com/minhnn/nexuslive-be/services/notification/internal/app"
)

func main() {
	cfg, generated, err := config.NewConfig()
	if err != nil {
		log.Fatalf("Config error: %s", err)
	}

	app.Run(cfg, generated)
}
