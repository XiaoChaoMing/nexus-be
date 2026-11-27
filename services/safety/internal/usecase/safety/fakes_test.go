package safety_test

import (
	"context"
	"sync"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/safety/internal/entity"
	"github.com/minhnn/nexuslive-be/services/safety/internal/repo"
	"github.com/minhnn/nexuslive-be/services/safety/internal/usecase/safety"
)

const (
	reporterID = "0199d4a2-0000-7000-8000-000000000001"
	authorID   = "0199d4a2-0000-7000-8000-000000000002"
	adminID    = "0199d4a2-0000-7000-8000-000000000003"
	admin2ID   = "0199d4a2-0000-7000-8000-000000000004"
	channelID  = "0199d4a2-0000-7000-8000-000000000010"
	streamID   = "0199d4a2-0000-7000-8000-000000000020"
	endedID    = "0199d4a2-0000-7000-8000-000000000021"
	vodEndedID = "0199d4a2-0000-7000-8000-000000000022"
	messageID  = "0199d4a2-0000-7000-8000-000000000030"
	reportID   = "0199d4a2-0000-7000-8000-000000000040"
	uploadID   = "0199d4a2-0000-7000-8000-000000000060"
	liveVODID  = "0199d4a2-0000-7000-8000-000000000061"
	deletedID  = "0199d4a2-0000-7000-8000-000000000062"
	remoteID   = "0199d4a2-0000-7000-8000-000000000063"
	downID     = "0199d4a2-0000-7000-8000-000000000064"
	missingID  = "0199d4a2-0000-7000-8000-0000000000ff"
)

var now = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC) //nolint:gochecknoglobals // test clock

type fakeReports struct {
	repo.ReportRepo

	dup      *entity.Report
	inserted []entity.Report
}

func (f *fakeReports) OpenDuplicate(context.Context, string, string, string) (*entity.Report, error) {
	return f.dup, nil
}

func (f *fakeReports) Insert(_ context.Context, r entity.Report) (entity.Report, bool, error) {
	r.ID, r.Status, r.CreatedAt = reportID, entity.ReportOpen, now
	f.inserted = append(f.inserted, r)

	return r, true, nil
}

type recorded struct {
	action    entity.AdminAction
	audit     entity.AuditEntry
	eventType string
	payload   any
}

type fakeActions struct {
	repo.ActionRepo

	mu   sync.Mutex
	recs []recorded
}

func (f *fakeActions) Record(_ context.Context, a entity.AdminAction, audit entity.AuditEntry, eventType string, payload any) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.recs = append(f.recs, recorded{action: a, audit: audit, eventType: eventType, payload: payload})

	return "action-1", nil
}

func (f *fakeActions) ReportExists(_ context.Context, id string) (bool, error) {
	return id == reportID, nil
}

type fakeReplica struct {
	repo.ReplicaRepo

	// kept are the VOD copies stored after a lookup in stream-svc.
	kept *[]repo.VODPatch
}

func (f fakeReplica) VOD(_ context.Context, id string) (entity.VODCopy, error) {
	switch id {
	case uploadID:
		return entity.VODCopy{ID: id, ChannelID: channelID, Source: entity.VODSourceUpload, Title: "Upload"}, nil
	case liveVODID:
		return entity.VODCopy{ID: id, ChannelID: channelID, StreamID: ptr(streamID), Source: entity.VODSourceLive, Title: "Rec"}, nil
	case deletedID:
		return entity.VODCopy{ID: id, ChannelID: channelID, Source: entity.VODSourceUpload, Deleted: true}, nil
	}

	return entity.VODCopy{}, entity.ErrNotFound
}

func (f fakeReplica) PatchVOD(_ context.Context, _ string, _ time.Time, p repo.VODPatch) error {
	*f.kept = append(*f.kept, p)

	return nil
}

// fakeVODs is stream-svc's internal API.
type fakeVODs struct{}

func (fakeVODs) VOD(_ context.Context, id string) (entity.VODCopy, error) {
	switch id {
	case remoteID:
		return entity.VODCopy{ID: id, ChannelID: channelID, Source: entity.VODSourceUpload, Title: "Remote"}, nil
	case downID:
		return entity.VODCopy{}, errDial
	}

	return entity.VODCopy{}, entity.ErrNotFound
}

func (fakeReplica) User(_ context.Context, id string) (entity.UserCopy, error) {
	switch id {
	case reporterID, authorID:
		return entity.UserCopy{ID: id, Username: "u" + id[len(id)-1:], GlobalRole: entity.RoleUser, Status: entity.UserActive}, nil
	case adminID, admin2ID:
		return entity.UserCopy{ID: id, Username: "admin", GlobalRole: entity.RoleAdmin, Status: entity.UserActive}, nil
	}

	return entity.UserCopy{}, entity.ErrNotFound
}

func (fakeReplica) Channel(_ context.Context, id string) (entity.ChannelCopy, error) {
	if id == channelID {
		return entity.ChannelCopy{ID: id, OwnerID: authorID, Handle: "an", DisplayName: "An"}, nil
	}

	return entity.ChannelCopy{}, entity.ErrNotFound
}

func (fakeReplica) Stream(_ context.Context, id string) (entity.StreamCopy, error) {
	vod := "0199d4a2-0000-7000-8000-000000000050"

	switch id {
	case streamID:
		return entity.StreamCopy{ID: id, ChannelID: channelID, Title: "Live", Status: entity.StreamLive}, nil
	case endedID:
		return entity.StreamCopy{ID: id, ChannelID: channelID, Status: entity.StreamEnded}, nil
	case vodEndedID:
		return entity.StreamCopy{ID: id, ChannelID: channelID, Status: entity.StreamEnded, VODID: &vod}, nil
	}

	return entity.StreamCopy{}, entity.ErrNotFound
}

type fakeRate struct {
	n   int
	ttl time.Duration
	hit int
}

func (f *fakeRate) Count(context.Context, string) (int, time.Duration, error) { return f.n, f.ttl, nil }

func (f *fakeRate) Hit(context.Context, string, time.Duration) error {
	f.hit++

	return nil
}

type fakeEvidence struct {
	items []entity.EvidenceItem
	err   error
	delay time.Duration
}

func (f fakeEvidence) Evidence(ctx context.Context, _, _, _ string, _ int) ([]entity.EvidenceItem, error) {
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(f.delay):
		}
	}

	return f.items, f.err
}

type env struct {
	uc      *safety.UseCase
	reports *fakeReports
	actions *fakeActions
	rate    *fakeRate
	kept    *[]repo.VODPatch
}

func newEnv(ev fakeEvidence, rate *fakeRate, dup *entity.Report) env {
	if rate == nil {
		rate = &fakeRate{}
	}

	e := env{reports: &fakeReports{dup: dup}, actions: &fakeActions{}, rate: rate, kept: &[]repo.VODPatch{}}
	e.uc = safety.New(safety.Repos{
		Reports: e.reports, Actions: e.actions, Replica: fakeReplica{kept: e.kept}, Rate: rate, Evidence: ev,
		VODs: fakeVODs{},
	}, logger.New("error"), safety.Clock(func() time.Time { return now }), safety.EvidenceTimeout(50*time.Millisecond))

	return e
}

func ptr[T any](v T) *T { return &v }
