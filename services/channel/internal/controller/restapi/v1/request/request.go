// Package request holds the JSON bodies accepted by channel-svc's public REST API.
package request

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
)

// NotifyLevel is the body of PUT /channels/{id}/follow.
type NotifyLevel struct {
	NotifyLevel string `json:"notifyLevel" enums:"ALL,LIVE_ONLY,OFF"`
}

// Validate -.
func (b NotifyLevel) Validate() map[string]string {
	if !entity.NotifyLevel(b.NotifyLevel).Valid() {
		return map[string]string{"notifyLevel": entity.DetailEnum}
	}

	return nil
}

// SocialLink -.
type SocialLink struct {
	Type string `json:"type" enums:"YOUTUBE,X,FACEBOOK,TIKTOK,DISCORD,WEBSITE"`
	URL  string `json:"url"`
}

// ChannelPatch is the body of PATCH /studio/channel; absent fields stay unchanged and "bio": null clears it.
type ChannelPatch struct {
	DisplayName   *string       `json:"displayName"`
	Bio           *string       `json:"bio"`
	SocialLinks   *[]SocialLink `json:"socialLinks"`
	MatureDefault *bool         `json:"matureDefault"`
	Handle        *string       `json:"handle"`
	// clearBio is set when the body has "bio": null.
	clearBio bool
}

// DecodeChannelPatch tells an absent bio from "bio": null; ok is false for a body that is not a JSON object.
func DecodeChannelPatch(body []byte) (ChannelPatch, bool) {
	var p ChannelPatch
	if err := json.Unmarshal(body, &p); err != nil {
		return ChannelPatch{}, false
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return ChannelPatch{}, false
	}

	if v, ok := raw["bio"]; ok && bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
		p.clearBio = true
	}

	return p, true
}

// Patch converts the body for the use case.
func (b ChannelPatch) Patch() entity.ProfilePatch {
	p := entity.ProfilePatch{
		DisplayName: b.DisplayName, Bio: b.Bio, ClearBio: b.clearBio, MatureDefault: b.MatureDefault,
		Handle: b.Handle,
	}

	if b.SocialLinks != nil {
		links := make([]entity.SocialLink, 0, len(*b.SocialLinks))
		for _, l := range *b.SocialLinks {
			links = append(links, entity.SocialLink(l))
		}

		p.SocialLinks = &links
	}

	return p
}

// Schedule is the body of POST /studio/schedules and PUT /studio/schedules/{id}.
type Schedule struct {
	Title      string  `json:"title"`
	CategoryID *string `json:"categoryId"`
	// StartAt is RFC 3339 with an offset (stored in UTC).
	StartAt     string `json:"startAt"`
	DurationMin int    `json:"durationMin"`
	// RRule: null / absent = one-off; FREQ=WEEKLY;BYDAY=MO,WE[;COUNT=n|;UNTIL=yyyymmddThhmmssZ].
	RRule *string `json:"rrule"`
	// Timezone is an IANA name (default Asia/Ho_Chi_Minh); weekly occurrences keep its wall-clock time.
	Timezone *string `json:"timezone"`
}

// Input converts the body; errs holds the format errors found here (startAt).
func (b Schedule) Input() (in entity.ScheduleInput, errs map[string]string) {
	in = entity.ScheduleInput{Title: b.Title, CategoryID: b.CategoryID, DurationMin: b.DurationMin}

	if b.RRule != nil {
		in.RRule = strings.TrimSpace(*b.RRule)
	}

	if b.Timezone != nil {
		in.Timezone = *b.Timezone
	}

	at, d := ParseInstant(b.StartAt)
	if d != "" {
		return in, map[string]string{"startAt": d}
	}

	in.StartAt = at

	return in, nil
}

// MoveOccurrence is the body of PUT /studio/schedules/{id}/occurrences/{occurrenceAt}.
type MoveOccurrence struct {
	StartAt string `json:"startAt"`
}

// Reminder is the body of POST /schedules/{id}/reminders.
type Reminder struct {
	OccurrenceAt string `json:"occurrenceAt"`
}

// SearchHistory is the body of POST /me/search-history.
type SearchHistory struct {
	Query string `json:"query"`
}

// ParseInstant parses an RFC 3339 instant; the second value is REQUIRED or FORMAT on failure.
func ParseInstant(s string) (t time.Time, detail string) {
	if strings.TrimSpace(s) == "" {
		return time.Time{}, entity.DetailRequired
	}

	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, entity.DetailFormat
	}

	return t.UTC(), ""
}
