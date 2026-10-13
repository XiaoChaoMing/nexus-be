package request

// Register -.
type Register struct {
	Username    string `json:"username"    validate:"required,username"`
	DisplayName string `json:"displayName" validate:"omitempty,max=50"`
	Email       string `json:"email"       validate:"required,email,max=254"`
	Password    string `json:"password"    validate:"required,password"`
	// Must be true (terms of service, 13+ self-declared).
	AcceptTerms bool `json:"acceptTerms" validate:"required"`
}

// Login -.
type Login struct {
	// Email or username.
	Login    string `json:"login"    validate:"required,max=254"`
	Password string `json:"password" validate:"required,max=72"`
}

// VerifyEmail -.
type VerifyEmail struct {
	Token string `json:"token" validate:"required,max=128"`
}

// ForgotPassword -.
type ForgotPassword struct {
	Email string `json:"email" validate:"required,email,max=254"`
}

// ResetPassword -.
type ResetPassword struct {
	Token    string `json:"token"    validate:"required,max=128"`
	Password string `json:"password" validate:"required,password"`
}
