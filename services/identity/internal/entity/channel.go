package entity

// UserChannel is identity's copy of a channel created by channel-svc (user_channels).
type UserChannel struct {
	UserID      string
	ChannelID   string
	Handle      string
	DisplayName string
}
