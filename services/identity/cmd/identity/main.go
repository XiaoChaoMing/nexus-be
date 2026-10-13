package main

import (
	"log"
	"os"
	_ "time/tzdata" // the scratch image has no zoneinfo; PATCH /v1/me validates IANA names and ages use Asia/Ho_Chi_Minh

	"github.com/minhnn/nexuslive-be/services/identity/config"
	"github.com/minhnn/nexuslive-be/services/identity/internal/app"
)

const (
	_cmdPromoteAdmin = "promote-admin"
	_promoteArgs     = 3
)

// Usage:
//
//	identity                       start the service
//	identity promote-admin <email> give an existing account the ADMIN role
func main() {
	cfg, err := config.NewConfig()
	if err != nil {
		log.Fatalf("Config error: %s", err)
	}

	if len(os.Args) > 1 {
		if os.Args[1] != _cmdPromoteAdmin || len(os.Args) != _promoteArgs {
			log.Fatalf("usage: identity [%s <email>]", _cmdPromoteAdmin)
		}

		os.Exit(app.PromoteAdmin(cfg, os.Args[2]))
	}

	app.Run(cfg)
}
