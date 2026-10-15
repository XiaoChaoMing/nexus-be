package v1

import (
	"github.com/go-playground/validator/v10"
	"github.com/minhnn/nexuslive-be/pkg/jwt"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase"
)

// V1 -.
type V1 struct {
	auth         usecase.Auth
	creator      usecase.Creator
	blocks       usecase.Blocks
	adminUsers   usecase.AdminUsers
	jwks         jwt.JWKS
	l            logger.Interface
	v            *validator.Validate
	cookieSecure bool
}
