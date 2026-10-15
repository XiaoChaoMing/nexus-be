package safety

import (
	"context"
	"fmt"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/safety/internal/entity"
	"github.com/minhnn/nexuslive-be/services/safety/internal/usecase"
)

// Lock durations an admin may pick (NXL-F §8); nil locks until unlocked.
var _lockHours = map[int]bool{24: true, 168: true, 720: true} //nolint:gochecknoglobals // static set

// action is one admin decision ready to record.
type action struct {
	kind, targetType, targetID, auditAction, eventType string
	reason                                             string
	reportID                                           *string
	expiresAt                                          *time.Time
	payload                                            any
}

// LockUser records LOCK_USER; identity applies it (safety.user_actioned).
func (uc *UseCase) LockUser(ctx context.Context, adminID, userID string, in usecase.LockInput) (string, error) {
	reason, err := uc.validateAction(ctx, in.Reason, in.ReportID, lockDetails(in.DurationHours, adminID, userID)...)
	if err != nil {
		return "", err
	}

	if err := uc.lockable(ctx, userID); err != nil {
		return "", err
	}

	var until *time.Time

	if in.DurationHours != nil {
		t := uc.now().Add(time.Duration(*in.DurationHours) * time.Hour).UTC()
		until = &t
	}

	return uc.record(ctx, adminID, action{
		kind: entity.ActionLockUser, targetType: entity.TargetUser, targetID: userID, auditAction: "USER_LOCKED",
		eventType: events.TypeUserActioned, reason: reason, reportID: in.ReportID, expiresAt: until,
		payload: events.UserActioned{UserID: userID, Action: events.ActionLock, LockedUntil: until, Reason: reason, AdminID: adminID},
	})
}

func lockDetails(hours *int, adminID, userID string) []string {
	details := []string{}
	if hours != nil && !_lockHours[*hours] {
		details = append(details, "durationHours", entity.DetailEnum)
	}

	if userID == adminID {
		details = append(details, "userId", entity.DetailInvalid)
	}

	return details
}

// lockable: the user exists in the copy and is not an admin.
func (uc *UseCase) lockable(ctx context.Context, userID string) error {
	u, err := uc.user(ctx, userID)
	if err != nil {
		return err
	}

	if u.GlobalRole == entity.RoleAdmin {
		return entity.ErrForbidden
	}

	return nil
}

func (uc *UseCase) user(ctx context.Context, id string) (entity.UserCopy, error) {
	if !isUUID(id) {
		return entity.UserCopy{}, entity.ErrNotFound
	}

	u, err := uc.r.Replica.User(ctx, id)
	if err != nil {
		return u, fmt.Errorf("adminactions - user: %w", err)
	}

	if u.Status == entity.UserDeleted {
		return u, entity.ErrNotFound
	}

	return u, nil
}

// UnlockUser records UNLOCK_USER; identity ignores a user who is not locked.
func (uc *UseCase) UnlockUser(ctx context.Context, adminID, userID string, in usecase.ActionInput) (string, error) {
	reason, err := uc.validateAction(ctx, in.Reason, in.ReportID)
	if err != nil {
		return "", err
	}

	if _, err := uc.user(ctx, userID); err != nil {
		return "", err
	}

	return uc.record(ctx, adminID, action{
		kind: entity.ActionUnlockUser, targetType: entity.TargetUser, targetID: userID, auditAction: "USER_UNLOCKED",
		eventType: events.TypeUserActioned, reason: reason, reportID: in.ReportID,
		payload: events.UserActioned{UserID: userID, Action: events.ActionUnlock, Reason: reason, AdminID: adminID},
	})
}

// SuspendChannel records SUSPEND_CHANNEL; channel-svc applies it (safety.channel_actioned).
func (uc *UseCase) SuspendChannel(ctx context.Context, adminID, channelID string, in usecase.ActionInput) (string, error) {
	return uc.channelAction(ctx, adminID, channelID, in, entity.ActionSuspendChannel, "CHANNEL_SUSPENDED", events.ActionSuspend)
}

// RestoreChannel records RESTORE_CHANNEL.
func (uc *UseCase) RestoreChannel(ctx context.Context, adminID, channelID string, in usecase.ActionInput) (string, error) {
	return uc.channelAction(ctx, adminID, channelID, in, entity.ActionRestoreChannel, "CHANNEL_RESTORED", events.ActionRestore)
}

func (uc *UseCase) channelAction(ctx context.Context, adminID, channelID string, in usecase.ActionInput,
	kind, auditAction, eventAction string,
) (string, error) {
	reason, err := uc.validateAction(ctx, in.Reason, in.ReportID)
	if err != nil {
		return "", err
	}

	if !isUUID(channelID) {
		return "", entity.ErrNotFound
	}

	if _, err := uc.r.Replica.Channel(ctx, channelID); err != nil {
		return "", fmt.Errorf("adminactions - channel: %w", err)
	}

	return uc.record(ctx, adminID, action{
		kind: kind, targetType: entity.TargetChannel, targetID: channelID, auditAction: auditAction,
		eventType: events.TypeChannelActioned, reason: reason, reportID: in.ReportID,
		payload: events.ChannelActioned{ChannelID: channelID, Action: eventAction, Reason: reason, AdminID: adminID},
	})
}

// TakedownStream records TAKEDOWN_STREAM for a stream on air or an ended one whose VOD still exists (BR-F23).
func (uc *UseCase) TakedownStream(ctx context.Context, adminID, streamID string, in usecase.ActionInput) (string, error) {
	reason, err := uc.validateAction(ctx, in.Reason, in.ReportID)
	if err != nil {
		return "", err
	}

	if !isUUID(streamID) {
		return "", entity.ErrNotFound
	}

	s, err := uc.r.Replica.Stream(ctx, streamID)
	if err != nil {
		return "", fmt.Errorf("adminactions - stream: %w", err)
	}

	if !s.TakedownAllowed() {
		return "", entity.ErrInvalidState
	}

	return uc.record(ctx, adminID, action{
		kind: entity.ActionTakedownStream, targetType: entity.TargetStream, targetID: streamID, auditAction: "STREAM_TAKEDOWN",
		eventType: events.TypeStreamTakedown, reason: reason, reportID: in.ReportID,
		payload: events.StreamTakedown{StreamID: streamID, ChannelID: s.ChannelID, Title: s.Title, Reason: reason, AdminID: adminID},
	})
}

// validateAction checks the reason (ADMIN_REASON_MIN/MAX) and the report id, plus extra details.
func (uc *UseCase) validateAction(ctx context.Context, reason string, reportID *string, extra ...string) (string, error) {
	reason, ok := trimmed(reason, entity.AdminReasonMin, entity.AdminReasonMax)

	details := extra
	if !ok {
		details = append(details, "reason", entity.DetailLength)
	}

	exists, err := uc.reportExists(ctx, reportID)
	if err != nil {
		return "", err
	}

	if !exists {
		details = append(details, "reportId", entity.DetailInvalid)
	}

	if len(details) > 0 {
		return "", entity.Reject(entity.ErrValidation, details...)
	}

	return reason, nil
}

// reportExists is true without a report id.
func (uc *UseCase) reportExists(ctx context.Context, reportID *string) (bool, error) {
	if reportID == nil {
		return true, nil
	}

	if !isUUID(*reportID) {
		return false, nil
	}

	ok, err := uc.r.Actions.ReportExists(ctx, *reportID)
	if err != nil {
		return false, fmt.Errorf("adminactions - report: %w", err)
	}

	return ok, nil
}

func (uc *UseCase) record(ctx context.Context, adminID string, a action) (string, error) {
	meta := map[string]any{}
	if a.reportID != nil {
		meta["reportId"] = *a.reportID
	}

	if a.expiresAt != nil {
		meta["expiresAt"] = a.expiresAt.Format(time.RFC3339)
	}

	id, err := uc.r.Actions.Record(ctx, entity.AdminAction{
		AdminID: adminID, ReportID: a.reportID, Type: a.kind, TargetType: a.targetType, TargetID: a.targetID,
		Reason: a.reason, ExpiresAt: a.expiresAt,
	}, entity.AuditEntry{
		ActorID: &adminID, ActorRole: events.ActorAdmin, Action: a.auditAction, ResourceType: a.targetType,
		ResourceID: &a.targetID, Reason: &a.reason, Metadata: meta,
	}, a.eventType, a.payload)
	if err != nil {
		return "", fmt.Errorf("adminactions - record: %w", err)
	}

	uc.l.Info("safety - audit=%s admin=%s target=%s action=%s", a.auditAction, adminID, a.targetID, id)

	return id, nil
}
