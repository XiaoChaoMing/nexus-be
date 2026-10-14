// Package request holds the request DTOs of chat-svc's public REST API.
package request

import (
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
}

// Validate checks the enumerations of NXL-B API 04 (the rule lives on the entity).
func (s UpdateChatSettings) Validate() map[string]string {
	errs := entity.DetailsOf(entity.ChatSettings{SlowSec: s.SlowSec, FollowersMin: s.FollowersMin}.Validate())
	if errs == nil {
		errs = map[string]string{}
	}

	return errs
}
