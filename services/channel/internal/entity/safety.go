package entity

import "time"

// ChannelCopy is what other services copy of a channel (internal snapshot).
type ChannelCopy struct {
	ID          string
	OwnerID     string
	Handle      string
	DisplayName string
	AvatarURL   *string
	Status      Status
	SuspendedAt *time.Time
	UpdatedAt   time.Time
}

// FollowCopy is what other services copy of a follow (internal snapshot).
type FollowCopy struct {
	UserID      string
	ChannelID   string
	NotifyLevel string
	CreatedAt   time.Time
}

// StatusChange is a suspension or a restore applied to a channel (safety.channel_actioned.v1).
type StatusChange struct {
	ChannelID string
	Suspend   bool
	Reason    string
	ActionID  string
}
