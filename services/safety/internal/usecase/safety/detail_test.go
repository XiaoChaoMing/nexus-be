package safety_test

import (
	"context"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/safety/internal/entity"
	"github.com/minhnn/nexuslive-be/services/safety/internal/repo"
	"github.com/minhnn/nexuslive-be/services/safety/internal/usecase"
	"github.com/minhnn/nexuslive-be/services/safety/internal/usecase/safety"
	"github.com/stretchr/testify/require"
)

const modID = "0199d4a2-0000-7000-8000-000000000005"

type detailReports struct {
	repo.ReportRepo

	report entity.Report
}

func (f detailReports) Get(context.Context, string) (entity.Report, error) { return f.report, nil }

func (detailReports) Related(context.Context, entity.Report, int) ([]entity.Report, error) {
	return nil, nil
}

type detailActions struct {
	repo.ActionRepo
}

func (detailActions) ForReport(context.Context, entity.Report, int) ([]entity.AdminAction, error) {
	return []entity.AdminAction{{
		ID: "a1", AdminID: adminID, Type: entity.ActionLockUser, TargetType: entity.TargetUser, TargetID: authorID,
		Reason: "spam", CreatedAt: now.Add(-time.Hour),
	}}, nil
}

type detailAudit struct {
	repo.AuditRepo

	filters *[]repo.AuditFilter
}

func (f detailAudit) List(_ context.Context, filter repo.AuditFilter, _ *repo.TimeCursor, _ int) ([]entity.AuditEntry, error) {
	*f.filters = append(*f.filters, filter)

	if filter.ActorID != nil {
		return []entity.AuditEntry{{ID: "e1", ActorID: filter.ActorID, Action: "USER_LOCKED", CreatedAt: now}}, nil
	}

	actor, reason := modID, "flood"

	return []entity.AuditEntry{{
		ID: "c1", ActorID: &actor, ActorRole: "MODERATOR", Action: entity.ActionChatTimeout, ResourceType: entity.TargetUser,
		ResourceID: filter.ResourceID, Reason: &reason, CreatedAt: now,
		Metadata: map[string]any{"channelId": channelID, "expiresAt": now.Add(10 * time.Minute).Format(time.RFC3339Nano)},
	}}, nil
}

type detailReplica struct {
	fakeReplica
}

func (detailReplica) Lookup(_ context.Context, ids repo.LookupIDs) (repo.Lookup, error) {
	l := repo.Lookup{
		Users: map[string]entity.UserCopy{}, Channels: map[string]entity.ChannelCopy{}, Streams: map[string]entity.StreamCopy{},
	}

	for _, id := range ids.Users {
		if id != missingID {
			l.Users[id] = entity.UserCopy{ID: id, Username: "u" + id[len(id)-1:], DisplayName: "U" + id[len(id)-1:]}
		}
	}

	for _, id := range ids.Streams {
		if id == streamID {
			l.Streams[id] = entity.StreamCopy{ID: id, ChannelID: channelID, Title: "Live"}
		}
	}

	return l, nil
}

func (detailReplica) SearchUsers(_ context.Context, q string, _ *string, _ *repo.NameCursor, _ int) ([]entity.UserCopy, error) {
	if q == "minh" {
		return []entity.UserCopy{{ID: adminID, Username: "Minh"}}, nil
	}

	return []entity.UserCopy{{ID: admin2ID, Username: q + "x"}}, nil
}

func detailEnv(r entity.Report, filters *[]repo.AuditFilter) *safety.UseCase {
	return safety.New(safety.Repos{
		Reports: detailReports{report: r}, Actions: detailActions{}, Audit: detailAudit{filters: filters}, Replica: detailReplica{},
	}, logger.New("error"), safety.Clock(func() time.Time { return now }))
}

func TestGetDetail(t *testing.T) {
	t.Parallel()

	author := authorID
	room := streamID
	r := entity.Report{
		ID: reportID, ReporterID: reporterID, TargetType: entity.TargetMessage, TargetID: messageID, TargetUserID: &author,
		StreamID: &room, Status: entity.ReportOpen, CreatedAt: now,
		Evidence: []entity.EvidenceItem{{MessageID: messageID, UserID: authorID}, {MessageID: "m0", UserID: missingID}},
	}

	var filters []repo.AuditFilter

	d, err := detailEnv(r, &filters).Get(context.Background(), reportID)
	require.NoError(t, err)

	require.Equal(t, channelID, *d.ChannelID, "channel of the room")
	require.Equal(t, "U2", d.Authors[authorID].DisplayName)
	require.NotContains(t, d.Authors, missingID)

	require.Len(t, filters, 1)
	require.Equal(t, entity.ChatRestrictionActions(), filters[0].Actions)
	require.Equal(t, authorID, *filters[0].ResourceID)

	require.Len(t, d.Actions, 2)
	chat, lock := d.Actions[0], d.Actions[1]
	require.Equal(t, entity.ActionChatTimeout, chat.Type, "newest first")
	require.Equal(t, "MODERATOR", chat.ActorRole)
	require.Equal(t, "u5", chat.Admin.Username)
	require.Equal(t, channelID, *chat.ChannelID)
	require.Equal(t, "flood", chat.Reason)
	require.Equal(t, now.Add(10*time.Minute), *chat.ExpiresAt)
	require.Equal(t, entity.RoleAdmin, lock.ActorRole)
	require.Nil(t, lock.ChannelID)
}

func TestGetDetailChannelOfTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		report entity.Report
		want   *string
	}{
		{name: "channel", report: entity.Report{TargetType: entity.TargetChannel, TargetID: channelID}, want: ptr(channelID)},
		{name: "stream", report: entity.Report{TargetType: entity.TargetStream, TargetID: streamID}, want: ptr(channelID)},
		{name: "unknown stream", report: entity.Report{TargetType: entity.TargetStream, TargetID: missingID}},
		{name: "user without room", report: entity.Report{TargetType: entity.TargetUser, TargetID: authorID}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var filters []repo.AuditFilter

			tc.report.ID, tc.report.ReporterID = reportID, reporterID

			d, err := detailEnv(tc.report, &filters).Get(context.Background(), reportID)
			require.NoError(t, err)
			require.Equal(t, tc.want, d.ChannelID)
			require.Empty(t, filters, "no user behind the target, no chat history")
		})
	}
}

func TestAuditActorUsername(t *testing.T) {
	t.Parallel()

	var filters []repo.AuditFilter

	audit := detailEnv(entity.Report{}, &filters).AuditLog()
	ctx := context.Background()

	p, err := audit.List(ctx, usecase.AuditQuery{Actor: "@minh"})
	require.NoError(t, err)
	require.Len(t, p.Items, 1)
	require.Equal(t, adminID, *filters[0].ActorID, "exact name, any case")

	p, err = audit.List(ctx, usecase.AuditQuery{Actor: "an"})
	require.NoError(t, err)
	require.Empty(t, p.Items, "only a prefix matched")
	require.Len(t, filters, 1, "no query for an unknown actor")
}
