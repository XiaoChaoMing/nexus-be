package entity

import "time"

// Restriction is a user's active moderation in a channel (mod_actions, written by moderation later).
type Restriction struct {
	Banned bool
	// TimedOutUntil is set while a timeout is running.
	TimedOutUntil *time.Time
}

// SendContext is everything PostgreSQL knows about a user writing in a stream's room, read in one query.
type SendContext struct {
	Room     Room
	Settings ChatSettings
	// OwnerID is empty when chat-svc never received channel.created for the channel.
	OwnerID     string
	Role        *ChannelRole
	Restriction Restriction
	// User is nil when the identity copy has no row for the user yet.
	User *User
}
