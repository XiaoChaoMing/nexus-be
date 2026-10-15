package v1

import (
	"encoding/json"
	"time"

	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/usecase"
)

// Data wraps every successful body.
type Data[T any] struct {
	Data T `json:"data"`
}

// ErrorResponse wraps every error body.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody -.
type ErrorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

// Notification is one item of the center; the client builds the sentence from type + payload (NXL-E §7.4).
type Notification struct {
	ID         string          `json:"id"`
	Type       string          `json:"type" enums:"CREATOR_LIVE,STREAM_STARTING,SCHEDULE,MENTION,NEW_FOLLOWER,CREATOR_APPLICATION,REPORT_RESULT,SYSTEM"`
	Payload    json.RawMessage `json:"payload" swaggertype:"object"`
	Link       *string         `json:"link"`
	GroupCount int             `json:"groupCount"`
	Read       bool            `json:"read"`
	CreatedAt  time.Time       `json:"createdAt"`
	UpdatedAt  time.Time       `json:"updatedAt"`
}

// NotificationList -.
type NotificationList struct {
	Items      []Notification `json:"items"`
	NextCursor *string        `json:"nextCursor"`
}

// UnreadCount -.
type UnreadCount struct {
	Count int `json:"count"`
}

// ReadAllRequest -; before (RFC 3339) defaults to now: only notifications updated at or before it are read.
type ReadAllRequest struct {
	Before *string `json:"before"`
}

// NotificationSetting is one row of the settings screen.
type NotificationSetting struct {
	Type           string `json:"type"`
	InApp          bool   `json:"inApp"`
	Email          bool   `json:"email"`
	InAppLocked    bool   `json:"inAppLocked"`
	EmailLocked    bool   `json:"emailLocked"`
	EmailAvailable bool   `json:"emailAvailable"`
}

// Settings -.
type Settings struct {
	Items         []NotificationSetting `json:"items"`
	EmailVerified bool                  `json:"emailVerified"`
}

// SettingPatch changes one type; an omitted switch keeps its value.
type SettingPatch struct {
	Type  string `json:"type" enums:"CREATOR_LIVE,STREAM_STARTING,SCHEDULE,MENTION,NEW_FOLLOWER,CREATOR_APPLICATION,REPORT_RESULT,SYSTEM"`
	InApp *bool  `json:"inApp"`
	Email *bool  `json:"email"`
}

// SettingsRequest is PUT /v1/me/notification-settings (1–8 rows).
type SettingsRequest struct {
	Items []SettingPatch `json:"items"`
}

// UnsubscribeRequest -.
type UnsubscribeRequest struct {
	Token string `json:"token"`
}

// Unsubscribed names the type whose e-mails were turned off.
type Unsubscribed struct {
	Type string `json:"type"`
}

func notificationOf(n entity.Notification) Notification {
	payload := n.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}

	return Notification{
		ID: n.ID, Type: string(n.Type), Payload: payload, Link: n.Link, GroupCount: n.GroupCount, Read: n.Read(),
		CreatedAt: n.CreatedAt.UTC(), UpdatedAt: n.UpdatedAt.UTC(),
	}
}

func settingsOf(v usecase.SettingsView) Settings {
	out := Settings{Items: make([]NotificationSetting, 0, len(v.Items)), EmailVerified: v.EmailVerified}
	for _, s := range v.Items {
		out.Items = append(out.Items, NotificationSetting{
			Type: string(s.Setting.Type), InApp: s.Setting.InApp, Email: s.Setting.Email, InAppLocked: s.Default.InAppLocked,
			EmailLocked: s.Default.EmailLocked, EmailAvailable: s.Default.EmailAvailable,
		})
	}

	return out
}
