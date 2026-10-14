package request

// SubmitApplication -.
type SubmitApplication struct {
	// Trimmed, 2-100 characters.
	LegalName string `json:"legalName" validate:"required,min=2,max=100"`
	// YYYY-MM-DD, not in the future.
	BirthDate string `json:"birthDate" validate:"required,datetime=2006-01-02"`
	Note      string `json:"note"      validate:"omitempty,max=500"`
	// Must be true.
	AcceptCreatorTerms bool `json:"acceptCreatorTerms" validate:"required"`
}

// ListApplications is the query of the admin list.
type ListApplications struct {
	Status string `query:"status" validate:"omitempty,oneof=PENDING APPROVED REJECTED CANCELLED ALL"`
	Cursor string `query:"cursor" validate:"omitempty,max=512"`
	Limit  *int   `query:"limit"  validate:"omitnil,min=1,max=50"`
}

// RejectApplication -.
type RejectApplication struct {
	// Trimmed, 10-500 characters; shown to the applicant.
	Reason string `json:"reason" validate:"required,min=10,max=500"`
}
