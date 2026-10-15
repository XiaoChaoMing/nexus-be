module github.com/minhnn/nexuslive-be/services/safety

go 1.27

// Built in workspace mode (../../go.work): packages under github.com/minhnn/nexuslive-be/pkg
// resolve to the root module, so it is not listed here.
require (
	github.com/caarlos0/env/v11 v11.4.1
	github.com/gofiber/fiber/v2 v2.52.15
	github.com/gofiber/swagger v1.1.1
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.11.0
	github.com/prometheus/client_golang v1.24.1
	github.com/redis/go-redis/v9 v9.17.3
	github.com/stretchr/testify v1.12.1
	github.com/swaggo/swag v1.16.6
)
