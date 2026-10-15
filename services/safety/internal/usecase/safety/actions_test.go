package safety_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/safety/internal/entity"
	"github.com/minhnn/nexuslive-be/services/safety/internal/usecase"
	"github.com/stretchr/testify/require"
)

const reason = "repeated spam in chat"

func TestLockUser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		user    string
		in      usecase.LockInput
		err     error
		details map[string]string
		until   *time.Time
	}{
		{
			name: "7 days", user: authorID, in: usecase.LockInput{DurationHours: ptr(168), Reason: reason, ReportID: ptr(reportID)},
			until: ptr(now.Add(168 * time.Hour)),
		},
		{name: "until unlocked", user: authorID, in: usecase.LockInput{Reason: reason}},
		{
			name: "self", user: adminID, in: usecase.LockInput{Reason: reason}, err: entity.ErrValidation,
			details: map[string]string{"userId": "INVALID"},
		},
		{name: "other admin", user: admin2ID, in: usecase.LockInput{Reason: reason}, err: entity.ErrForbidden},
		{name: "missing", user: missingID, in: usecase.LockInput{Reason: reason}, err: entity.ErrNotFound},
		{name: "bad id", user: "x", in: usecase.LockInput{Reason: reason}, err: entity.ErrNotFound},
		{
			name: "short reason, bad duration, unknown report", user: authorID,
			in: usecase.LockInput{DurationHours: ptr(5), Reason: " spam  ", ReportID: ptr(missingID)}, err: entity.ErrValidation,
			details: map[string]string{"durationHours": "ENUM", "reason": "LENGTH", "reportId": "INVALID"},
		},
		{
			name: "long reason", user: authorID, in: usecase.LockInput{Reason: strings.Repeat("a", 501)}, err: entity.ErrValidation,
			details: map[string]string{"reason": "LENGTH"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(fakeEvidence{}, nil, nil)
			id, err := e.uc.LockUser(context.Background(), adminID, tc.user, tc.in)

			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.Equal(t, tc.details, entity.DetailsOf(err))
				require.Empty(t, e.actions.recs)

				return
			}

			require.NoError(t, err)
			require.Equal(t, "action-1", id)
			require.Len(t, e.actions.recs, 1)

			rec := e.actions.recs[0]
			require.Equal(t, entity.ActionLockUser, rec.action.Type)
			require.Equal(t, tc.until, rec.action.ExpiresAt)
			require.Equal(t, "USER_LOCKED", rec.audit.Action)
			require.Equal(t, events.ActorAdmin, rec.audit.ActorRole)
			require.Equal(t, events.TypeUserActioned, rec.eventType)
			require.Equal(t, events.UserActioned{
				UserID: tc.user, Action: events.ActionLock, LockedUntil: tc.until,
				Reason: reason, AdminID: adminID,
			}, rec.payload)
		})
	}
}

func TestTakedown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		stream string
		err    error
	}{
		{name: "live", stream: streamID},
		{name: "ended with vod", stream: vodEndedID},
		{name: "ended without vod", stream: endedID, err: entity.ErrInvalidState},
		{name: "missing", stream: missingID, err: entity.ErrNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(fakeEvidence{}, nil, nil)
			_, err := e.uc.TakedownStream(context.Background(), adminID, tc.stream, usecase.ActionInput{Reason: reason})

			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			require.Equal(t, events.TypeStreamTakedown, e.actions.recs[0].eventType)
			p, ok := e.actions.recs[0].payload.(events.StreamTakedown)
			require.True(t, ok)
			require.Equal(t, channelID, p.ChannelID)
		})
	}
}

func TestChannelActions(t *testing.T) {
	t.Parallel()

	e := newEnv(fakeEvidence{}, nil, nil)
	ctx := context.Background()

	_, err := e.uc.SuspendChannel(ctx, adminID, channelID, usecase.ActionInput{Reason: reason})
	require.NoError(t, err)
	_, err = e.uc.RestoreChannel(ctx, adminID, channelID, usecase.ActionInput{Reason: reason})
	require.NoError(t, err)
	_, err = e.uc.UnlockUser(ctx, adminID, authorID, usecase.ActionInput{Reason: reason})
	require.NoError(t, err)
	_, err = e.uc.SuspendChannel(ctx, adminID, missingID, usecase.ActionInput{Reason: reason})
	require.ErrorIs(t, err, entity.ErrNotFound)

	require.Len(t, e.actions.recs, 3)
	require.Equal(t, events.ChannelActioned{ChannelID: channelID, Action: events.ActionSuspend, Reason: reason, AdminID: adminID},
		e.actions.recs[0].payload)
	require.Equal(t, "CHANNEL_RESTORED", e.actions.recs[1].audit.Action)
	require.Equal(t, entity.ActionUnlockUser, e.actions.recs[2].action.Type)
}

func TestResolveValidation(t *testing.T) {
	t.Parallel()

	e := newEnv(fakeEvidence{}, nil, nil)
	_, _, err := e.uc.Resolve(context.Background(), adminID, reportID, usecase.ResolveInput{Status: "OPEN", Note: "short"})
	require.ErrorIs(t, err, entity.ErrValidation)
	require.Equal(t, map[string]string{"status": "ENUM", "note": "LENGTH"}, entity.DetailsOf(err))
}

func TestCursorsAndQueries(t *testing.T) {
	t.Parallel()

	e := newEnv(fakeEvidence{}, nil, nil)
	ctx := context.Background()

	_, err := e.uc.List(ctx, usecase.ReportQuery{Cursor: "%%%"})
	require.Equal(t, map[string]string{"cursor": "INVALID"}, entity.DetailsOf(err))

	_, err = e.uc.List(ctx, usecase.ReportQuery{Statuses: []string{"OPEN", "NOPE"}, Reasons: []string{"X"}})
	require.Equal(t, map[string]string{"status": "ENUM", "reason": "ENUM"}, entity.DetailsOf(err))

	_, err = e.uc.Users(ctx, usecase.DirectoryQuery{Q: strings.Repeat("a", 51), Status: ptr("BANNED")})
	require.Equal(t, map[string]string{"q": "LENGTH", "status": "ENUM"}, entity.DetailsOf(err))

	_, err = e.uc.AuditLog().List(ctx, usecase.AuditQuery{Cursor: "abc"})
	require.Equal(t, map[string]string{"cursor": "INVALID"}, entity.DetailsOf(err))
}
