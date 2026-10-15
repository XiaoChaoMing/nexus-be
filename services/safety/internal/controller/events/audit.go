package events

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/safety/internal/entity"
)

const _adminPromoted = "ADMIN_PROMOTED"

// auditEntry maps a moderation or audit event to one audit row (TSD §6.5); promote names the user an
// ADMIN_PROMOTED row makes admin.
func auditEntry(e eventbus.Event) (entity.AuditEntry, *string, error) {
	var (
		entry entity.AuditEntry
		err   error
	)

	switch e.Type {
	case events.TypeChatModAction:
		entry, err = modAction(e)
	case events.TypeChatSettingsChanged:
		entry, err = settingsChanged(e)
	case events.TypeChannelRoleChanged:
		entry, err = roleChanged(e)
	default:
		entry, err = recorded(e)
	}

	if err != nil {
		return entry, nil, err
	}

	id := e.ID
	entry.SourceEventID, entry.SourceService = &id, e.Producer

	var promote *string
	if entry.Action == _adminPromoted && entry.ResourceType == entity.TargetUser && entry.ResourceID != nil {
		promote = entry.ResourceID
	}

	return entry, promote, nil
}

func modAction(e eventbus.Event) (entity.AuditEntry, error) {
	var p events.ChatModAction
	if err := decode(e, &p, &p.ChannelID, &p.ActorID); err != nil {
		return entity.AuditEntry{}, err
	}

	meta := map[string]any{"channelId": p.ChannelID}
	putOpt(meta, "streamId", p.StreamID)
	putOpt(meta, "messageBody", p.MessageBody)

	if p.ExpiresAt != nil {
		meta["expiresAt"] = p.ExpiresAt
	}

	resType, resID := entity.TargetUser, validID(p.TargetUserID)

	switch p.Type {
	case events.ModDelete, events.ModPin, events.ModUnpin:
		resType, resID = entity.TargetMessage, validID(p.MessageID)
		putOpt(meta, "targetUserId", p.TargetUserID)
	case events.ModTimeout, events.ModUntimeout, events.ModBan, events.ModUnban:
	default:
		return entity.AuditEntry{}, fmt.Errorf("%w: %s: type %q", eventbus.ErrSkip, e.Type, p.Type)
	}

	return entity.AuditEntry{
		ActorID: &p.ActorID, ActorRole: p.ActorRole, Action: "CHAT_" + p.Type, ResourceType: resType, ResourceID: resID,
		Reason: p.Reason, Metadata: meta,
	}, nil
}

func settingsChanged(e eventbus.Event) (entity.AuditEntry, error) {
	var p events.ChatSettingsChanged
	if err := decode(e, &p, &p.ChannelID, &p.ActorID); err != nil {
		return entity.AuditEntry{}, err
	}

	return entity.AuditEntry{
		ActorID: &p.ActorID, ActorRole: p.ActorRole, Action: "CHAT_SETTINGS_CHANGED", ResourceType: entity.TargetChannel,
		ResourceID: &p.ChannelID, Metadata: map[string]any{"before": p.Before, "after": p.After},
	}, nil
}

func roleChanged(e eventbus.Event) (entity.AuditEntry, error) {
	var p events.ChannelRoleChanged
	if err := decode(e, &p, &p.ChannelID, &p.UserID, &p.ActorID); err != nil {
		return entity.AuditEntry{}, err
	}

	action := "CHANNEL_ROLE_GRANTED"
	if p.Role == nil {
		action = "CHANNEL_ROLE_REVOKED"
	}

	return entity.AuditEntry{
		ActorID: &p.ActorID, ActorRole: p.ActorRole, Action: action, ResourceType: entity.TargetUser, ResourceID: &p.UserID,
		Metadata: map[string]any{"channelId": p.ChannelID, "role": p.Role, "previousRole": p.PreviousRole},
	}, nil
}

func recorded(e eventbus.Event) (entity.AuditEntry, error) {
	var p events.AuditRecorded
	if err := decode(e, &p); err != nil {
		return entity.AuditEntry{}, err
	}

	if p.Action == "" || p.ResourceType == "" {
		return entity.AuditEntry{}, fmt.Errorf("%w: %s: no action", eventbus.ErrSkip, e.Type)
	}

	meta := p.Metadata
	if meta == nil {
		meta = map[string]any{}
	}

	putOpt(meta, "requestId", p.RequestID)

	return entity.AuditEntry{
		ActorID: validID(p.ActorID), ActorRole: p.ActorRole, Action: entity.Excerpt(p.Action, 50), //nolint:mnd // column size
		ResourceType: entity.Excerpt(p.ResourceType, 30), ResourceID: validID(p.ResourceID), Reason: p.Reason, //nolint:mnd // column size
		Metadata: meta,
	}, nil
}

func putOpt(m map[string]any, key string, v *string) {
	if v != nil {
		m[key] = *v
	}
}

// validID drops an id that is not a uuid.
func validID(id *string) *string {
	if id == nil || uuid.Validate(*id) != nil {
		return nil
	}

	return id
}
