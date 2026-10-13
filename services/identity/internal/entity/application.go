package entity

import "time"

// ApplicationStatus mirrors creator_applications.status.
type ApplicationStatus string

// Creator application statuses.
const (
	ApplicationPending   ApplicationStatus = "PENDING"
	ApplicationApproved  ApplicationStatus = "APPROVED"
	ApplicationRejected  ApplicationStatus = "REJECTED"
	ApplicationCancelled ApplicationStatus = "CANCELLED" //nolint:misspell // value of the database CHECK and the API
)

// Application is a creator application in nxl_identity.creator_applications.
type Application struct {
	ID           string
	UserID       string
	Status       ApplicationStatus
	LegalName    string
	BirthDate    time.Time
	Note         string
	RejectReason string
	ReviewedBy   *string
	ReviewedAt   *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
	// Applicant and Age are filled for the admin views only.
	Applicant *User
	Age       int
}

// AgeOn returns the age in full years of someone born on birth, on the calendar day of today.
// Both are compared as calendar dates; today should already be in the reference time zone.
func AgeOn(birth, today time.Time) int {
	age := today.Year() - birth.Year()

	if today.Month() < birth.Month() || (today.Month() == birth.Month() && today.Day() < birth.Day()) {
		age--
	}

	return age
}
