package entity_test

import (
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/stretchr/testify/require"
)

func TestVideoFormatOK(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, file, contentType string
		ok                      bool
	}{
		{name: "mp4", file: "a.mp4", contentType: "video/mp4", ok: true},
		{name: "upper-case extension", file: "IMG_1.MOV", contentType: "video/quicktime", ok: true},
		{name: "no type", file: "a.webm", ok: true},
		{name: "octet-stream", file: "a.mkv", contentType: "application/octet-stream", ok: true},
		{name: "other extension", file: "a.avi", contentType: "video/mp4"},
		{name: "no extension", file: "mp4", contentType: "video/mp4"},
		{name: "other type", file: "a.mp4", contentType: "image/png"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.ok, entity.VideoFormatOK(tc.file, tc.contentType))
		})
	}
}

func TestVODRules(t *testing.T) {
	t.Parallel()

	stream := "0199d4a2-0000-7000-8000-000000000001"
	desc, title := "d", "t"

	for _, tc := range []struct {
		name string
		vod  entity.VOD
		p    entity.VODPatch
		want error
	}{
		{name: "live ready title", vod: entity.VOD{Source: entity.SourceLive, Status: entity.VODReady}, p: entity.VODPatch{Title: &title}},
		{
			name: "live expired", vod: entity.VOD{Source: entity.SourceLive, Status: entity.VODExpired}, p: entity.VODPatch{Title: &title},
			want: entity.ErrInvalidState,
		},
		{
			name: "live description", vod: entity.VOD{Source: entity.SourceLive, Status: entity.VODReady},
			p: entity.VODPatch{Description: &desc}, want: entity.ErrInvalidState,
		},
		{name: "upload processing", vod: entity.VOD{Source: entity.SourceUpload, Status: entity.VODProcessing}, p: entity.VODPatch{Description: &desc}},
		{name: "upload ready", vod: entity.VOD{Source: entity.SourceUpload, Status: entity.VODReady}, p: entity.VODPatch{Title: &title}},
		{
			name: "upload failed", vod: entity.VOD{Source: entity.SourceUpload, Status: entity.VODFailed}, p: entity.VODPatch{Title: &title},
			want: entity.ErrInvalidState,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.ErrorIs(t, tc.vod.CanApply(tc.p), tc.want)
		})
	}

	require.Equal(t, "streams/"+stream+"/", entity.VOD{ID: "v", StreamID: &stream, Source: entity.SourceLive}.PlaylistPrefix())
	require.Equal(t, "videos/v/", entity.VOD{ID: "v", Source: entity.SourceUpload}.PlaylistPrefix())
	require.True(t, entity.VOD{Source: entity.SourceUpload, Status: entity.VODFailed, FailureRetryable: true}.Retryable())
	require.False(t, entity.VOD{Source: entity.SourceUpload, Status: entity.VODFailed}.Retryable())
}

func TestUploadParts(t *testing.T) {
	t.Parallel()

	const part = 8 << 20

	u := entity.Upload{SizeBytes: 2*part + 5, PartSize: part, PartCount: entity.PartCount(2*part+5, part)}
	require.Equal(t, 3, u.PartCount)
	require.Equal(t, 1, entity.PartCount(part, part))
	require.Zero(t, entity.PartCount(0, part))

	require.NoError(t, u.CheckPart(1, part))
	require.NoError(t, u.CheckPart(3, 5))
	require.ErrorIs(t, u.CheckPart(4, 5), entity.ErrInvalidPart)

	var pe *entity.InvalidPartError
	require.ErrorAs(t, u.CheckPart(3, part), &pe)
	require.Equal(t, int64(5), pe.ExpectedBytes)

	u.Parts = map[int]entity.UploadPart{3: {ETag: "c"}, 1: {ETag: "a"}}
	require.Equal(t, []int{1, 3}, u.UploadedParts())
	require.Equal(t, []int{2}, u.MissingParts())
	require.Equal(t, []entity.UploadPartRef{{Number: 1, ETag: "a"}, {Number: 3, ETag: "c"}}, u.PartRefs())
}

func TestUploadReceiving(t *testing.T) {
	t.Parallel()

	now := time.Now()
	u := entity.Upload{Status: entity.UploadUploading, ExpiresAt: now.Add(time.Minute)}
	require.NoError(t, u.Receiving(now))
	require.ErrorIs(t, u.Receiving(now.Add(time.Minute)), entity.ErrUploadExpired)

	u.Status = entity.UploadAborted
	require.ErrorIs(t, u.Receiving(now), entity.ErrUploadExpired)

	u.Status = entity.UploadCompleted
	require.ErrorIs(t, u.Receiving(now), entity.ErrInvalidState)
}

func TestUploadQuota(t *testing.T) {
	t.Parallel()

	now := time.Now()
	l := entity.UploadLimits{Active: 3, Daily: 10}
	oldest := now.Add(-20 * time.Hour)

	require.NoError(t, entity.UploadQuota{Active: 2, Daily: 9}.Check(l, now))

	var le *entity.UploadLimitError
	require.ErrorAs(t, entity.UploadQuota{Active: 3}.Check(l, now), &le)
	require.Equal(t, entity.UploadLimitActive, le.Limit)

	err := entity.UploadQuota{Daily: 10, OldestDaily: &oldest}.Check(l, now)
	require.ErrorIs(t, err, entity.ErrUploadLimit)
	require.ErrorAs(t, err, &le)
	require.Equal(t, entity.UploadLimitDaily, le.Limit)
	require.Equal(t, 4*time.Hour, le.RetryAfter)
}
