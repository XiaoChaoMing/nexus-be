package safety_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/services/safety/internal/entity"
	"github.com/minhnn/nexuslive-be/services/safety/internal/usecase"
	"github.com/stretchr/testify/require"
)

var errDial = errors.New("dial")

func messageEvidence(author string) []entity.EvidenceItem {
	return []entity.EvidenceItem{
		{MessageID: "0199d4a2-0000-7000-8000-000000000031", UserID: author, Body: "earlier"},
		{MessageID: messageID, UserID: author, Body: "spam spam"},
	}
}

func TestFile(t *testing.T) { //nolint:funlen // table
	t.Parallel()

	msg := usecase.ReportInput{TargetType: entity.TargetMessage, TargetID: messageID, Reason: entity.ReasonSpam, StreamID: ptr(streamID)}

	tests := []struct {
		name     string
		in       usecase.ReportInput
		ev       fakeEvidence
		rate     *fakeRate
		dup      *entity.Report
		err      error
		details  map[string]string
		created  bool
		evStatus string
		target   *string
	}{
		{
			name: "message captured", in: msg, ev: fakeEvidence{items: messageEvidence(authorID)}, created: true,
			evStatus: entity.EvidenceCaptured, target: ptr(authorID),
		},
		{
			name: "own message", in: msg, ev: fakeEvidence{items: messageEvidence(reporterID)},
			err: entity.ErrValidation, details: map[string]string{"targetId": "INVALID"},
		},
		{name: "message missing", in: msg, ev: fakeEvidence{err: entity.ErrNotFound}, err: entity.ErrNotFound},
		{name: "chat down", in: msg, ev: fakeEvidence{err: errDial}, created: true, evStatus: entity.EvidenceUnavailable},
		{
			name: "chat slow", in: msg, ev: fakeEvidence{items: messageEvidence(authorID), delay: time.Second}, created: true,
			evStatus: entity.EvidenceUnavailable,
		},
		{name: "duplicate", in: msg, dup: &entity.Report{ID: "old", Status: entity.ReportInReview}},
		{name: "rate limited", in: msg, rate: &fakeRate{n: 10, ttl: 90 * time.Second}, err: entity.ErrRateLimited},
		{name: "duplicate beats rate", in: msg, rate: &fakeRate{n: 10}, dup: &entity.Report{ID: "old"}},
		{
			name: "user", in: usecase.ReportInput{TargetType: entity.TargetUser, TargetID: authorID, Reason: entity.ReasonHarassment},
			created: true, evStatus: entity.EvidenceNone, target: ptr(authorID),
		},
		{
			name: "self", in: usecase.ReportInput{TargetType: entity.TargetUser, TargetID: reporterID, Reason: entity.ReasonSpam},
			err: entity.ErrValidation, details: map[string]string{"targetId": "INVALID"},
		},
		{
			name: "unknown user", in: usecase.ReportInput{TargetType: entity.TargetUser, TargetID: missingID, Reason: entity.ReasonSpam},
			err: entity.ErrNotFound,
		},
		{
			name: "channel", in: usecase.ReportInput{TargetType: entity.TargetChannel, TargetID: channelID, Reason: entity.ReasonOther},
			created: true, evStatus: entity.EvidenceNone, target: ptr(authorID),
		},
		{
			name: "stream", in: usecase.ReportInput{TargetType: entity.TargetStream, TargetID: streamID, Reason: entity.ReasonViolence},
			created: true, evStatus: entity.EvidenceNone, target: ptr(authorID),
		},
		{
			name: "uploaded video", in: usecase.ReportInput{TargetType: entity.TargetVOD, TargetID: uploadID, Reason: entity.ReasonCopyright},
			created: true, evStatus: entity.EvidenceNone, target: ptr(authorID),
		},
		{
			name: "recording", in: usecase.ReportInput{TargetType: entity.TargetVOD, TargetID: liveVODID, Reason: entity.ReasonSpam},
			created: true, evStatus: entity.EvidenceNone, target: ptr(authorID),
		},
		{
			name: "vod from stream-svc", in: usecase.ReportInput{TargetType: entity.TargetVOD, TargetID: remoteID, Reason: entity.ReasonSpam},
			created: true, evStatus: entity.EvidenceNone, target: ptr(authorID),
		},
		{
			name: "deleted vod", in: usecase.ReportInput{TargetType: entity.TargetVOD, TargetID: deletedID, Reason: entity.ReasonSpam},
			err: entity.ErrNotFound,
		},
		{
			name: "unknown vod", in: usecase.ReportInput{TargetType: entity.TargetVOD, TargetID: missingID, Reason: entity.ReasonSpam},
			err: entity.ErrNotFound,
		},
		{
			name: "validation", in: usecase.ReportInput{TargetType: "X", TargetID: "nope", Note: ptr(string(make([]byte, 501)))},
			err: entity.ErrValidation, details: map[string]string{
				"targetType": "ENUM", "targetId": "INVALID", "reason": "REQUIRED", "note": "LENGTH",
			},
		},
		{
			name: "message needs stream", in: usecase.ReportInput{TargetType: entity.TargetMessage, TargetID: messageID, Reason: entity.ReasonSpam},
			err: entity.ErrValidation, details: map[string]string{"streamId": "REQUIRED"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(tc.ev, tc.rate, tc.dup)
			r, created, err := e.uc.File(context.Background(), reporterID, tc.in)

			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.Equal(t, tc.details, entity.DetailsOf(err))
				require.Empty(t, e.reports.inserted)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.created, created)

			if !created {
				require.Equal(t, tc.dup.ID, r.ID)
				require.Zero(t, e.rate.hit)

				return
			}

			require.Equal(t, 1, e.rate.hit)
			require.Equal(t, tc.evStatus, r.EvidenceStatus)
			require.Equal(t, tc.target, r.TargetUserID)
		})
	}
}

func TestFileRetryAfter(t *testing.T) {
	t.Parallel()

	e := newEnv(fakeEvidence{}, &fakeRate{n: 10, ttl: 1500 * time.Millisecond}, nil)
	_, _, err := e.uc.File(context.Background(), reporterID,
		usecase.ReportInput{TargetType: entity.TargetUser, TargetID: authorID, Reason: entity.ReasonSpam})

	var rl *entity.RateLimitError
	require.ErrorAs(t, err, &rl)
	require.Equal(t, 1500*time.Millisecond, rl.RetryAfter)
}

func TestFileVOD(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	in := func(id string) usecase.ReportInput {
		return usecase.ReportInput{TargetType: entity.TargetVOD, TargetID: id, Reason: entity.ReasonSpam, StreamID: ptr(endedID)}
	}

	e := newEnv(fakeEvidence{}, nil, nil)

	r, _, err := e.uc.File(ctx, reporterID, in(uploadID))
	require.NoError(t, err)
	require.Nil(t, r.StreamID, "an uploaded video has no stream")

	r, _, err = e.uc.File(ctx, reporterID, in(liveVODID))
	require.NoError(t, err)
	require.Equal(t, streamID, *r.StreamID, "a recording keeps its stream")

	_, _, err = e.uc.File(ctx, reporterID, in(remoteID))
	require.NoError(t, err)
	require.Len(t, *e.kept, 1, "the VOD read from stream-svc is kept in the copy")
	require.Equal(t, remoteID, (*e.kept)[0].ID)
	require.Equal(t, "Remote", *(*e.kept)[0].Title)

	_, _, err = e.uc.File(ctx, reporterID, in(downID))
	require.ErrorIs(t, err, errDial)
	require.NotErrorIs(t, err, entity.ErrNotFound)
}
