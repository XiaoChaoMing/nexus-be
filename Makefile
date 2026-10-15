ifneq ($(wildcard .env),)
include .env
export
else
$(warning WARNING: .env file not found! Using .env.example)
include .env.example
export
endif

BASE_STACK = docker compose -f docker-compose.yml
ALL_PROFILES = --profile nxl --profile obs

# Go modules of the workspace (go.work). Add a service here when it gets its own module.
NXL_GO_MODULES := . services/identity services/channel services/stream services/media services/chat services/safety
# -race needs cgo (gcc). Without a C compiler on Windows: make nxl-test NXL_TEST_FLAGS=
NXL_TEST_FLAGS ?= -race

# One recipe line per module (no shell loop, so it also runs under cmd.exe).
define nl


endef

# HELP =================================================================================================================
# This will output the help for each task
# thanks to https://marmelab.com/blog/2016/02/29/auto-documented-makefile.html
.PHONY: help

help: ## Display this help screen
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

# STACK ================================================================================================================

nxl-infra-up: ### start infrastructure only: Postgres, NATS, MinIO, Redis, Mailpit (UI http://localhost:8025)
	$(BASE_STACK) up -d db nats minio redis mailpit
.PHONY: nxl-infra-up

nxl-obs-up: ### start Prometheus :9090, Grafana :3000 (dashboards "NexusLive — Livestream" and "NexusLive — Chat") and Jaeger :16686
	$(BASE_STACK) --profile obs up -d
.PHONY: nxl-obs-up

nxl-up-all: ### start everything behind the Nginx edge on http://localhost:$${NXL_EDGE_PORT:-8080}, plus observability
	$(BASE_STACK) $(ALL_PROFILES) up --build -d
.PHONY: nxl-up-all

nxl-down: ### stop every container of the stack (volumes are kept)
	$(BASE_STACK) $(ALL_PROFILES) down --remove-orphans
.PHONY: nxl-down

nxl-build: ### build the service images
	$(BASE_STACK) --profile nxl build
.PHONY: nxl-build

nxl-edge-logs: ### follow the Nginx edge access log (JSON, one line per request)
	$(BASE_STACK) logs -f nginx
.PHONY: nxl-edge-logs

# QUALITY ==============================================================================================================

nxl-lint: ### golangci-lint on every Go module of the workspace
	$(foreach m,$(NXL_GO_MODULES),go -C $(m) tool golangci-lint run ./...$(nl))
.PHONY: nxl-lint

nxl-test: ### unit tests of every Go module; repository tests also need NXL_TEST_PG_URL_<SVC> (stream, channel, chat, identity, safety), Redis tests NXL_TEST_REDIS_ADDR
	$(foreach m,$(NXL_GO_MODULES),go -C $(m) test $(NXL_TEST_FLAGS) -covermode atomic ./...$(nl))
.PHONY: nxl-test

linter-golangci: nxl-lint ### alias of nxl-lint
.PHONY: linter-golangci

test: nxl-test ### alias of nxl-test
.PHONY: test

# Services import pkg/ through go.work only, so `go mod tidy` cannot run inside them (it would look
# for github.com/minhnn/nexuslive-be on the network); their go.mod is edited by hand.
deps: ### tidy + verify the root module (pkg/ and tools)
	go mod tidy
	go mod verify

.PHONY: deps

deps-audit: ### check dependencies vulnerabilities
	$(foreach m,$(NXL_GO_MODULES),go -C $(m) tool govulncheck ./...$(nl))
.PHONY: deps-audit

format: ### Run code formatter (gofumpt, gci, goimports as configured in .golangci.yml)
	go tool golangci-lint fmt ./...
.PHONY: format

linter-hadolint: ### check by hadolint linter
	git ls-files --exclude='Dockerfile*' --ignored | xargs hadolint
.PHONY: linter-hadolint

linter-dotenv: ### check by dotenv linter
	dotenv-linter
.PHONY: linter-dotenv

# SERVICES =============================================================================================================

NXL_SERVICES := identity channel stream chat safety notification billing

nxl-db-init: ### create the NexusLive databases if missing
	$(BASE_STACK) --profile nxl run --rm db-init
.PHONY: nxl-db-init

nxl-migrate-up: ### apply NexusLive migrations to every service database
	$(BASE_STACK) --profile nxl run --rm db-migrate
.PHONY: nxl-migrate-up

nxl-migrate-down: ### roll back every migration of one service: make nxl-migrate-down SVC=chat
	@case " $(NXL_SERVICES) " in *" $(SVC) "*) ;; *) echo "SVC must be one of: $(NXL_SERVICES)"; exit 1;; esac
	$(BASE_STACK) --profile nxl run --rm db-migrate 'migrate -path /services/$(SVC)/migrations -database "$$NXL_PG_BASE/nxl_$(SVC)?sslmode=disable" down -all'
.PHONY: nxl-migrate-down

nxl-up: ### start postgres + migrations, NATS, Mailpit, identity-svc :8090, channel-svc :8094 and stream-svc :8092
	$(BASE_STACK) --profile nxl up --build -d db db-init db-migrate nats mailpit identity channel stream pgadmin
.PHONY: nxl-up

nxl-pgadmin: ### start pgAdmin on http://127.0.0.1:5050 (server "NexusLive (db)" is preconfigured)
	$(BASE_STACK) --profile nxl up -d db pgadmin
.PHONY: nxl-pgadmin

nxl-identity-run: ### run identity-svc locally with Go 1.27 (database from make nxl-up)
	go run ./services/identity/cmd/identity
.PHONY: nxl-identity-run

nxl-promote-admin: ### give an existing account the ADMIN role: make nxl-promote-admin EMAIL=a@b.c (database from make nxl-up)
	@test -n "$(EMAIL)" || { echo "EMAIL is required"; exit 1; }
	$(BASE_STACK) --profile nxl run --rm --no-deps identity promote-admin '$(EMAIL)'
.PHONY: nxl-promote-admin

nxl-identity-test: ### unit tests of identity-svc and the shared jwt package
	go test -v -race -covermode atomic ./services/identity/... ./pkg/jwt/...
.PHONY: nxl-identity-test

nxl-media-up: ### start the livestream stack: identity, channel-svc, stream-svc, MinIO, media-svc (RTMP :1935, http://localhost:8091/dev/player)
	$(BASE_STACK) --profile nxl up --build -d db db-init db-migrate nats mailpit identity channel stream minio media
.PHONY: nxl-media-up

nxl-media-logs: ### follow media-svc logs
	$(BASE_STACK) logs -f media
.PHONY: nxl-media-logs

nxl-media-publish: ### publish a 60 s test pattern instead of OBS: make nxl-media-publish KEY=live_... (needs ffmpeg)
	ffmpeg -hide_banner -re -f lavfi -i testsrc2=size=1280x720:rate=30 -f lavfi -i sine=frequency=440:sample_rate=48000 \
		-vf "drawtext=text='%{localtime\:%T}':fontsize=48:x=20:y=20:fontcolor=white:box=1" \
		-c:v libx264 -preset veryfast -tune zerolatency -g 60 -c:a aac -b:a 128k -t 60 \
		-f flv rtmp://localhost:1935/live/$(KEY)
.PHONY: nxl-media-publish

nxl-stream-test: ### stream-svc tests (repository tests need NXL_TEST_PG_URL_STREAM, see the test file)
	go test -v -race -covermode atomic ./services/stream/... ./pkg/eventbus/... ./pkg/outbox/... ./pkg/jwt/...
.PHONY: nxl-stream-test

nxl-chat-test: ### chat-svc tests (repository tests need NXL_TEST_PG_URL_CHAT, Redis tests NXL_TEST_REDIS_ADDR)
	go test -v -race -covermode atomic ./services/chat/... ./pkg/redis/... ./pkg/eventbus/... ./pkg/outbox/...
.PHONY: nxl-chat-test

nxl-chat-up: ### start the chat stack: identity, channel, stream, Redis and two chat-svc instances (chat-1, chat-2 :8095) behind the edge
	$(BASE_STACK) --profile nxl up --build -d db db-init db-migrate nats mailpit redis identity channel stream chat-1 chat-2 nginx
.PHONY: nxl-chat-up

nxl-chat-dev-grant: ### demo follow/sub/mod/vip/owner: make nxl-chat-dev-grant GRANT=follow CHANNEL=<uuid> USER_ID=<uuid> [ARGS="--minutes 60"]
	@test -n "$(GRANT)" -a -n "$(CHANNEL)" -a -n "$(USER_ID)" || { echo "GRANT, CHANNEL and USER_ID are required"; exit 1; }
	$(BASE_STACK) --profile nxl run --rm --no-deps chat-1 dev-grant $(GRANT) --channel $(CHANNEL) --user $(USER_ID) $(ARGS)
.PHONY: nxl-chat-dev-grant

nxl-channel-test: ### channel-svc tests (repository tests need NXL_TEST_PG_URL_CHANNEL, see the test file)
	go test -v -race -covermode atomic ./services/channel/... ./pkg/eventbus/... ./pkg/outbox/...
.PHONY: nxl-channel-test

nxl-media-test: ### unit tests of media-svc
	go test -v -race -covermode atomic ./services/media/...
.PHONY: nxl-media-test

nxl-bench: ### transcode load test through the edge: make nxl-bench COUNTS="1 2 3" (needs ffmpeg, docker; scripts/measure-latency.md §6)
	COUNTS="$(or $(COUNTS),1 2 3)" bash scripts/bench-transcode.sh
.PHONY: nxl-bench

nxl-swag: ### regenerate Swagger specs of identity-svc, channel-svc, stream-svc, chat-svc and safety-svc (runs swag in Docker, no local Go needed)
	docker run --rm -v "$(CURDIR):/src" -w /src golang:1.27-alpine3.23 sh -c 'for s in identity channel stream chat safety; do \
		(cd services/$$s && go tool swag init -q -g internal/controller/restapi/router.go -o docs \
			--parseInternal --parseDependency --parseDepth 2 --useStructName) || exit 1; done'
.PHONY: nxl-swag

nxl-migrate-create: ### create a migration: make nxl-migrate-create SVC=chat NAME=add_x
	@case " $(NXL_SERVICES) " in *" $(SVC) "*) ;; *) echo "SVC must be one of: $(NXL_SERVICES)"; exit 1;; esac
	@test -n "$(NAME)" || { echo "NAME is required"; exit 1; }
	migrate create -ext sql -dir services/$(SVC)/migrations '$(NAME)'
.PHONY: nxl-migrate-create

bin-deps: ### install tools
	go install tool
	go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate
.PHONY: bin-deps

pre-commit: format nxl-lint nxl-test ### run pre-commit
.PHONY: pre-commit
