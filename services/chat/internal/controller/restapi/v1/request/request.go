// Package request holds the request DTOs of chat-svc's public REST API.
package request

import (
	"regexp"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
)

// History is the query of GET /chat/rooms/{streamId}/messages.
type History struct {
	// Before is a message id (the nextCursor of the previous page).
	Before string `query:"before"`
	// Limit is 1–100 (default CHAT_HISTORY_ON_JOIN).
	Limit int `query:"limit"`
}

const _maxLimit = 100

// Validate returns the field errors (NXL-00 §4.1).
func (h History) Validate() map[string]string {
	errs := map[string]string{}

	if h.Before != "" {
		if _, err := uuid.Parse(h.Before); err != nil {
			errs["before"] = "INVALID"
		}
	}

	if h.Limit < 0 || h.Limit > _maxLimit {
		errs["limit"] = "MAX"
	}

	return errs
}

// UpdateChatSettings is the body of PUT /channels/{channelId}/chat-settings.
type UpdateChatSettings struct {
	// SlowSec ∈ {0, 2, 5, 10, 30, 60, 120}.
	SlowSec int `json:"slowSec"`
	// FollowersMin ∈ {null (off), 0, 10, 30, 60, 1440, 10080, 43200, 129600} minutes.
	FollowersMin *int `json:"followersMin"`
	SubsOnly     bool `json:"subsOnly"`
	// BlockLinks refuses messages with links (owner, moderators, admins and VIPs exempt); absent = unchanged.
	BlockLinks *bool `json:"blockLinks"`
}

// Validate checks the enumerations of NXL-B API 04 (the rule lives on the entity).
func (s UpdateChatSettings) Validate() map[string]string {
	errs := entity.DetailsOf(entity.ChatSettings{SlowSec: s.SlowSec, FollowersMin: s.FollowersMin}.Validate())
	if errs == nil {
		errs = map[string]string{}
	}

	return errs
}

// Timeout is the body of PUT /channels/{channelId}/timeouts/{userId}.
type Timeout struct {
	// DurationSec is 1 … 1209600 (14 days); the web offers 60, 600 and 3600.
	DurationSec *int `json:"durationSec"`
	// Reason is at most 500 characters (internal, never shown to the user).
	Reason *string `json:"reason"`
}

// Validate -.
func (t Timeout) Validate() map[string]string {
	errs := map[string]string{}

	switch {
	case t.DurationSec == nil:
		errs["durationSec"] = entity.DetailRequired
	case *t.DurationSec < 1:
		errs["durationSec"] = "MIN"
	}

	return errs
}

// Ban is the body of PUT /channels/{channelId}/bans/{userId}.
type Ban struct {
	Reason *string `json:"reason"`
}

// Pin is the body of PUT /chat/rooms/{streamId}/pin.
type Pin struct {
	MessageID string `json:"messageId"`
}

// Validate -.
func (p Pin) Validate() map[string]string {
	if p.MessageID == "" {
		return map[string]string{"messageId": entity.DetailRequired}
	}

	if _, err := uuid.Parse(p.MessageID); err != nil {
		return map[string]string{"messageId": entity.DetailInvalid}
	}

	return map[string]string{}
}

// BlockedTerm is the body of POST /channels/{channelId}/blocked-terms.
type BlockedTerm struct {
	// Term is 2–100 characters; it matches whole words, case and diacritics ignored.
	Term string `json:"term"`
}

// AssignRole is the body of POST /channels/{channelId}/roles.
type AssignRole struct {
	Username string `json:"username"`
	// Role is MODERATOR or VIP.
	Role string `json:"role"`
}

var _usernameRe = regexp.MustCompile(`^[A-Za-z0-9_]{3,25}$`)

// Validate -.
func (a AssignRole) Validate() map[string]string {
	errs := map[string]string{}

	switch {
	case a.Username == "":
		errs["username"] = entity.DetailRequired
	case !_usernameRe.MatchString(a.Username):
		errs["username"] = entity.DetailInvalid
	}

	if !entity.ChannelRole(a.Role).Valid() {
		errs["role"] = entity.DetailEnum
	}

	return errs
}

// SetRole is the body of PUT /channels/{channelId}/roles/{userId}.
type SetRole struct {
	Role string `json:"role"`
}

// Validate -.
func (s SetRole) Validate() map[string]string {
	if !entity.ChannelRole(s.Role).Valid() {
		return map[string]string{"role": entity.DetailEnum}
	}

	return map[string]string{}
}

// Page is the query of the moderation lists.
type Page struct {
	// Type filters restrictions (BAN | TIMEOUT); Role filters roles (MODERATOR | VIP).
	Type   string `query:"type"`
	Role   string `query:"role"`
	Cursor string `query:"cursor"`
	// Limit is 1–50 (default 24).
	Limit int `query:"limit"`
}

const _maxPage = 50

// Validate -.
func (p Page) Validate() map[string]string {
	errs := map[string]string{}

	if p.Type != "" && !entity.RestrictionType(p.Type).Valid() {
		errs["type"] = entity.DetailEnum
	}

	if p.Role != "" && !entity.ChannelRole(p.Role).Valid() {
		errs["role"] = entity.DetailEnum
	}

	if p.Limit < 0 || p.Limit > _maxPage {
		errs["limit"] = "MAX"
	}

	return errs
}
