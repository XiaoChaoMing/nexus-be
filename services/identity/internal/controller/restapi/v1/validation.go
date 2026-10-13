package v1

import (
	"errors"
	"regexp"
	"strings"
	"unicode"

	"github.com/go-playground/validator/v10"
)

const (
	_passwordMin = 8
	_passwordMax = 72
)

var _usernameRe = regexp.MustCompile(`^[A-Za-z0-9_]{3,25}$`)

// NewValidator registers the identity-specific rules; they mirror the web form and the users table CHECK.
func NewValidator() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())

	_ = v.RegisterValidation("username", func(fl validator.FieldLevel) bool { //nolint:errcheck // static tag
		return _usernameRe.MatchString(fl.Field().String())
	})

	_ = v.RegisterValidation("password", func(fl validator.FieldLevel) bool { //nolint:errcheck // static tag
		return validPassword(fl.Field().String())
	})

	return v
}

func validPassword(p string) bool {
	if len(p) < _passwordMin || len(p) > _passwordMax {
		return false
	}

	var letter, digit bool

	for _, c := range p {
		switch {
		case unicode.IsLetter(c):
			letter = true
		case unicode.IsDigit(c):
			digit = true
		}
	}

	return letter && digit
}

// fieldErrors maps each failed field (by JSON name) to a stable code the client can translate.
func fieldErrors(err error) map[string]string {
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return nil
	}

	out := make(map[string]string, len(verrs))

	for _, fe := range verrs {
		name := fe.Field()
		if name != "" {
			name = strings.ToLower(name[:1]) + name[1:]
		}

		out[name] = strings.ToUpper(fe.Tag())
	}

	return out
}
