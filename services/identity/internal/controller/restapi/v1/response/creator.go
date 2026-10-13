package response

import (
	"time"

	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase"
)

// Application is a creator application as its applicant sees it.
type Application struct {
	ID     string `json:"id"`
	Status string `json:"status"    example:"PENDING"`
	// Applicant's legal name.
	LegalName string `json:"legalName"`
	// YYYY-MM-DD.
	BirthDate    string     `json:"birthDate" example:"2000-01-31"`
	Note         *string    `json:"note"`
	RejectReason *string    `json:"rejectReason"`
	CreatedAt    time.Time  `json:"createdAt"`
	ReviewedAt   *time.Time `json:"reviewedAt"`
}

// Applicant is the account behind an application (admin views).
type Applicant struct {
	ID            string    `json:"id"`
	Username      string    `json:"username"`
	DisplayName   string    `json:"displayName"`
	Email         string    `json:"email"`
	EmailVerified bool      `json:"emailVerified"`
	CreatedAt     time.Time `json:"createdAt"`
}

// AdminApplication adds the applicant and their age today (Asia/Ho_Chi_Minh calendar day).
type AdminApplication struct {
	Application

	Age  int       `json:"age"`
	User Applicant `json:"user"`
}

// ApplicationPage is one page of the admin list, oldest first.
type ApplicationPage struct {
	Items []AdminApplication `json:"items"`
	// Null on the last page.
	NextCursor *string `json:"nextCursor"`
}

// NewApplication -.
func NewApplication(a entity.Application) Application {
	return Application{
		ID:           a.ID,
		Status:       string(a.Status),
		LegalName:    a.LegalName,
		BirthDate:    a.BirthDate.Format(entity.DateLayout),
		Note:         optional(a.Note),
		RejectReason: optional(a.RejectReason),
		CreatedAt:    a.CreatedAt.UTC(),
		ReviewedAt:   utc(a.ReviewedAt),
	}
}

// NewAdminApplication -.
func NewAdminApplication(a entity.Application) AdminApplication {
	out := AdminApplication{Application: NewApplication(a), Age: a.Age}

	if u := a.Applicant; u != nil {
		out.User = Applicant{
			ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Email: u.Email,
			EmailVerified: u.IsEmailVerified(), CreatedAt: u.CreatedAt.UTC(),
		}
	}

	return out
}

// NewApplicationPage -.
func NewApplicationPage(p usecase.ApplicationPage) ApplicationPage {
	out := ApplicationPage{Items: make([]AdminApplication, 0, len(p.Items))}

	for _, a := range p.Items {
		out.Items = append(out.Items, NewAdminApplication(a))
	}

	if p.NextCursor != "" {
		next := p.NextCursor
		out.NextCursor = &next
	}

	return out
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}

	u := t.UTC()

	return &u
}

func optional(s string) *string {
	if s == "" {
		return nil
	}

	return &s
}
