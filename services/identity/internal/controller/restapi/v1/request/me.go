package request

// Preferences -.
type Preferences struct {
	SidebarCollapsed *bool `json:"sidebarCollapsed"`
	MatureVisible    *bool `json:"matureVisible"`
}

// UpdateMe changes only the fields that are present.
type UpdateMe struct {
	// Trimmed, 1-50 characters.
	DisplayName *string `json:"displayName" validate:"omitnil,min=1,max=50"`
	// YYYY-MM-DD, not in the future; can only be set while empty.
	BirthDate *string `json:"birthDate" validate:"omitnil,datetime=2006-01-02"`
	Locale    *string `json:"locale"    validate:"omitnil,oneof=vi en"`
	// IANA name, e.g. Asia/Ho_Chi_Minh.
	Timezone    *string      `json:"timezone"    validate:"omitnil,timezone,max=50"`
	Preferences *Preferences `json:"preferences"`
}

// ChangePassword -.
type ChangePassword struct {
	CurrentPassword string `json:"currentPassword" validate:"required,max=72"`
	NewPassword     string `json:"newPassword"     validate:"required,password"`
}
