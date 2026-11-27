package persistent_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo/persistent"
	"github.com/stretchr/testify/require"
)

const _bucket = "nxl-uploads"

var _limits = entity.UploadLimits{Active: 3, Daily: 10} //nolint:gochecknoglobals // fixture

// newSession inserts an UPLOADING session of two parts for the channel.
func newSession(t *testing.T, r *persistent.Repo, ch string) entity.Upload {
	t.Helper()

	id := uuid.NewString()
	u, err := r.CreateUpload(context.Background(), entity.Upload{
		ID: id, ChannelID: ch, UserID: uuid.NewString(), FileName: "a.mp4", ContentType: "video/mp4", SizeBytes: 8<<20 + 100,
		PartSize: 8 << 20, PartCount: 2, S3UploadID: "s3", ObjectKey: entity.UploadSourceKey(id), Title: "Học Go",
		Description: "buổi 1", Visibility: entity.VisibilityPublic, ExpiresAt: time.Now().Add(time.Hour),
	}, _limits)
	require.NoError(t, err)

	return u
}

// completed uploads both parts of a new session and completes it; returns the session and its VOD id.
func completed(t *testing.T, r *persistent.Repo, ch string) (u entity.Upload, vodID string) {
	t.Helper()

	ctx := context.Background()
	u = newSession(t, r, ch)

	_, err := r.SavePart(ctx, u.ID, 1, entity.UploadPart{ETag: "a", Size: 8 << 20})
	require.NoError(t, err)
	_, err = r.SavePart(ctx, u.ID, 2, entity.UploadPart{ETag: "b", Size: 100})
	require.NoError(t, err)

	u, err = r.CompleteUpload(ctx, u.ID, _bucket)
	require.NoError(t, err)
	require.NotNil(t, u.VODID)

	return u, *u.VODID
}

func studioVOD(t *testing.T, r *persistent.Repo, ch, id string) entity.VOD {
	t.Helper()

	list, err := r.ListByChannel(context.Background(), ch, false, repo.StudioVODFilter{}, nil, 50)
	require.NoError(t, err)

	for _, v := range list {
		if v.ID == id {
			return v
		}
	}

	require.Failf(t, "vod not listed", "%s", id)

	return entity.VOD{}
}

func countType(t *testing.T, pg *postgres.Postgres, eventType string) int {
	t.Helper()

	n := 0

	for _, typ := range outboxTypes(t, pg) {
		if typ == eventType {
			n++
		}
	}

	return n
}

func TestUploadToReady(t *testing.T) { //nolint:paralleltest,funlen // shared database; one story
	r, pg := setup(t)
	ctx := context.Background()
	ch := uuid.NewString()

	u := newSession(t, r, ch)
	require.Equal(t, entity.UploadUploading, u.Status)
	require.Empty(t, u.Parts)

	_, err := r.GetUpload(ctx, uuid.NewString(), u.ID)
	require.ErrorIs(t, err, entity.ErrNotFound, "another channel's session")

	// Parts merge into the jsonb; sending a part again replaces it.
	_, err = r.SavePart(ctx, u.ID, 2, entity.UploadPart{ETag: "b0", Size: 100})
	require.NoError(t, err)
	_, err = r.SavePart(ctx, u.ID, 2, entity.UploadPart{ETag: "b", Size: 100})
	require.NoError(t, err)
	got, err := r.SavePart(ctx, u.ID, 1, entity.UploadPart{ETag: "a", Size: 8 << 20})
	require.NoError(t, err)
	require.Equal(t, map[int]entity.UploadPart{1: {ETag: "a", Size: 8 << 20}, 2: {ETag: "b", Size: 100}}, got.Parts)
	require.Empty(t, got.MissingParts())

	done, err := r.CompleteUpload(ctx, u.ID, _bucket)
	require.NoError(t, err)
	require.Equal(t, entity.UploadCompleted, done.Status)

	again, err := r.CompleteUpload(ctx, u.ID, _bucket)
	require.NoError(t, err)
	require.Equal(t, *done.VODID, *again.VODID, "completing twice keeps the same VOD")
	require.Equal(t, 1, countType(t, pg, events.TypeUploadCompleted))

	queued := lastPayload[events.UploadCompleted](t, pg, events.TypeUploadCompleted)
	require.Equal(t, events.UploadCompleted{
		UploadID: u.ID, VODID: *done.VODID, ChannelID: ch, Bucket: _bucket, ObjectKey: "sources/" + u.ID, SizeBytes: u.SizeBytes,
	}, queued)

	saved, err := r.SavePart(ctx, u.ID, 1, entity.UploadPart{ETag: "x", Size: 1})
	require.NoError(t, err)
	require.Equal(t, entity.UploadCompleted, saved.Status, "a completed session takes no more parts")
	require.Equal(t, "a", saved.Parts[1].ETag)

	vodID := *done.VODID
	v := studioVOD(t, r, ch, vodID)
	require.Equal(t, entity.VODProcessing, v.Status)
	require.Equal(t, entity.SourceUpload, v.Source)
	require.Nil(t, v.StreamID)
	require.Nil(t, v.ExpiresAt)
	require.Nil(t, v.PublishedAt)
	require.Equal(t, "buổi 1", v.Description)

	_, err = r.GetVOD(ctx, vodID)
	require.ErrorIs(t, err, entity.ErrNotFound, "not public while processing")

	// Progress only goes up.
	require.NoError(t, r.SetProgress(ctx, vodID, 40, events.UploadStageTranscoding))
	require.NoError(t, r.SetProgress(ctx, vodID, 20, events.UploadStageProbing))
	v = studioVOD(t, r, ch, vodID)
	require.Equal(t, 40, v.ProcessingPercent)
	require.Equal(t, events.UploadStageTranscoding, *v.ProcessingStage)

	in := repo.UploadReadyInput{
		VODID: vodID, DurationSec: 125, PlaylistPath: "videos/" + vodID + "/master.m3u8", ThumbnailPath: "videos/" + vodID + "/thumb.jpg",
	}
	out, err := r.MarkVODReady(ctx, in)
	require.NoError(t, err)
	require.Equal(t, repo.UploadResultReady, out.Result)
	require.Equal(t, u.ID, out.Upload.ID)

	out, err = r.MarkVODReady(ctx, in)
	require.NoError(t, err)
	require.Equal(t, repo.UploadResultIgnored, out.Result, "redelivery")
	require.Equal(t, 1, countType(t, pg, events.TypeVODPublished))

	published := lastPayload[events.VODPublished](t, pg, events.TypeVODPublished)
	require.Equal(t, entity.SourceUpload, published.Source)
	require.Equal(t, 125, published.DurationSec)
	require.Equal(t, "/videos/"+vodID+"/thumb.jpg", published.ThumbnailURL)
	require.False(t, published.PublishedAt.IsZero())

	// Public everywhere, never expires.
	got2, err := r.GetVOD(ctx, vodID)
	require.NoError(t, err)
	require.Equal(t, entity.VODReady, got2.Status)
	require.NotNil(t, got2.PublishedAt)

	n, err := r.ExpireVODs(ctx)
	require.NoError(t, err)
	require.Zero(t, n)

	uploadSrc, liveSrc := entity.SourceUpload, entity.SourceLive
	list, err := r.ListByChannel(ctx, ch, true, repo.StudioVODFilter{Source: &uploadSrc}, nil, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)

	list, err = r.ListByChannel(ctx, ch, true, repo.StudioVODFilter{Source: &liveSrc}, nil, 10)
	require.NoError(t, err)
	require.Empty(t, list)

	public, err := r.ListPublic(ctx, repo.VODFilter{}, false, nil, 10)
	require.NoError(t, err)
	require.Len(t, public, 1)

	// The source is purged once.
	purge, err := r.SourcesToPurge(ctx, 10)
	require.NoError(t, err)
	require.Len(t, purge, 1)
	require.NoError(t, r.MarkSourceDeleted(ctx, u.ID))
	purge, err = r.SourcesToPurge(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, purge)

	// Deleting announces the videos/ prefix; a late upload.processed announces it again.
	require.NoError(t, r.DeleteVOD(ctx, ch, vodID))

	deleted := lastPayload[events.VODDeleted](t, pg, events.TypeVODDeleted)
	require.Equal(t, events.VODDeleted{
		VODID: vodID, ChannelID: ch, Source: entity.SourceUpload, PlaylistPrefix: "videos/" + vodID + "/",
	}, deleted)

	out, err = r.MarkVODReady(ctx, in)
	require.NoError(t, err)
	require.Equal(t, repo.UploadResultDeleted, out.Result)
	require.Equal(t, 2, countType(t, pg, events.TypeVODDeleted))
}

func TestUploadEdits(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	ch := uuid.NewString()
	_, vodID := completed(t, r, ch)

	desc, cat, mature := "mới", uuid.NewString(), true
	v, err := r.UpdateVOD(ctx, ch, vodID, entity.VODPatch{Description: &desc, CategoryID: &cat, IsMature: &mature})
	require.NoError(t, err, "a processing upload can be described")
	require.Equal(t, cat, *v.CategoryID)
	require.True(t, v.IsMature)
	require.Zero(t, countType(t, pg, events.TypeVODUpdated), "not announced before it is published")

	_, err = r.MarkVODReady(ctx, repo.UploadReadyInput{VODID: vodID, PlaylistPath: "videos/" + vodID + "/master.m3u8"})
	require.NoError(t, err)

	empty := ""
	v, err = r.UpdateVOD(ctx, ch, vodID, entity.VODPatch{CategoryID: &empty})
	require.NoError(t, err)
	require.Nil(t, v.CategoryID, `"" removes the category`)
	require.Empty(t, v.ThumbnailURL, "no thumbnail written")

	updated := lastPayload[events.VODUpdated](t, pg, events.TypeVODUpdated)
	require.Equal(t, entity.SourceUpload, updated.Source)
	require.Equal(t, "mới", updated.Description)
	require.Empty(t, updated.StreamID)
	require.Nil(t, updated.ExpiresAt)

	live := vod(t, r, ch, false, nil)
	_, err = r.UpdateVOD(ctx, ch, live.ID, entity.VODPatch{Description: &desc})
	require.ErrorIs(t, err, entity.ErrInvalidState, "a broadcast VOD has no description")
}

func TestUploadFailAndRetry(t *testing.T) { //nolint:paralleltest,funlen // shared database; one story
	r, pg := setup(t)
	ctx := context.Background()
	ch := uuid.NewString()
	u, vodID := completed(t, r, ch)

	require.NoError(t, r.SetProgress(ctx, vodID, 70, events.UploadStageTranscoding))

	ok, err := r.MarkVODFailed(ctx, vodID, events.UploadFailStorageError, true)
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = r.MarkVODFailed(ctx, vodID, events.UploadFailStorageError, true)
	require.NoError(t, err)
	require.False(t, ok, "only a PROCESSING upload fails")

	failedStatus := entity.VODFailed
	list, err := r.ListByChannel(ctx, ch, false, repo.StudioVODFilter{Status: &failedStatus}, nil, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, events.UploadFailStorageError, *list[0].FailureReason)
	require.True(t, list[0].FailureRetryable)

	title := "x"
	_, err = r.UpdateVOD(ctx, ch, vodID, entity.VODPatch{Title: &title})
	require.ErrorIs(t, err, entity.ErrInvalidState, "a FAILED upload cannot be edited")

	_, err = r.RetryVOD(ctx, uuid.NewString(), vodID, _bucket)
	require.ErrorIs(t, err, entity.ErrNotFound, "another channel")

	v, err := r.RetryVOD(ctx, ch, vodID, _bucket)
	require.NoError(t, err)
	require.Equal(t, entity.VODProcessing, v.Status)
	require.Zero(t, v.ProcessingPercent, "progress starts over")
	require.Nil(t, v.FailureReason)
	require.Equal(t, 2, countType(t, pg, events.TypeUploadCompleted))

	of, err := r.UploadOfVOD(ctx, ch, vodID)
	require.NoError(t, err)
	require.Equal(t, u.ID, of.ID)

	ok, err = r.MarkVODFailed(ctx, vodID, events.UploadFailNoVideoStream, false)
	require.NoError(t, err)
	require.True(t, ok)

	_, err = r.RetryVOD(ctx, ch, vodID, _bucket)
	require.ErrorIs(t, err, entity.ErrInvalidState, "not retryable")

	// An admin takedown deletes it once per event.
	ev := uuid.NewString()
	found, err := r.TakedownVOD(ctx, ev, vodID)
	require.NoError(t, err)
	require.True(t, found)

	found, err = r.TakedownVOD(ctx, ev, vodID)
	require.NoError(t, err)
	require.False(t, found, "redelivery")

	found, err = r.TakedownVOD(ctx, uuid.NewString(), vodID)
	require.NoError(t, err)
	require.False(t, found, "already deleted")
	require.Equal(t, 1, countType(t, pg, events.TypeVODDeleted))
	require.Equal(t, "videos/"+vodID+"/", lastPayload[events.VODDeleted](t, pg, events.TypeVODDeleted).PlaylistPrefix)

	cp, err := r.VODCopy(ctx, vodID)
	require.NoError(t, err)
	require.Equal(t, entity.VODDeleted, cp.Status)

	_, err = r.VODCopy(ctx, uuid.NewString())
	require.ErrorIs(t, err, entity.ErrNotFound)
}

func TestUploadQuotaAndExpiry(t *testing.T) { //nolint:paralleltest,funlen // shared database; one story
	r, pg := setup(t)
	ctx := context.Background()
	ch := uuid.NewString()

	a := newSession(t, r, ch)
	b := newSession(t, r, ch)
	_, _ = completed(t, r, ch) // a PROCESSING video counts as active

	q, err := r.UploadQuota(ctx, ch)
	require.NoError(t, err)
	require.Equal(t, 3, q.Active)
	require.Equal(t, 3, q.Daily)
	require.NotNil(t, q.OldestDaily)

	_, err = r.CreateUpload(ctx, entity.Upload{ChannelID: ch}, _limits)

	var le *entity.UploadLimitError
	require.ErrorAs(t, err, &le)
	require.Equal(t, entity.UploadLimitActive, le.Limit)

	// Abort is idempotent and frees a slot.
	got, ok, err := r.AbortUpload(ctx, ch, a.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, entity.UploadAborted, got.Status)

	got, ok, err = r.AbortUpload(ctx, ch, a.ID)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, entity.UploadAborted, got.Status)

	_, _, err = r.AbortUpload(ctx, uuid.NewString(), b.ID)
	require.ErrorIs(t, err, entity.ErrNotFound)

	// Expiry picks sessions past expires_at only.
	_, err = pg.Pool.Exec(ctx, `UPDATE video_uploads SET expires_at = now() - interval '1 minute' WHERE id = $1`, b.ID)
	require.NoError(t, err)

	expired, err := r.ExpireUploads(ctx, 10)
	require.NoError(t, err)
	require.Len(t, expired, 1)
	require.Equal(t, b.ID, expired[0].ID)

	_, err = r.CompleteUpload(ctx, b.ID, _bucket)
	require.ErrorIs(t, err, entity.ErrUploadExpired)

	// A suspension aborts what is still uploading.
	c := newSession(t, r, ch)
	aborted, err := r.AbortChannelUploads(ctx, ch)
	require.NoError(t, err)
	require.Len(t, aborted, 1)
	require.Equal(t, c.ID, aborted[0].ID)

	// The daily quota counts every session of the last 24 h.
	_, err = pg.Pool.Exec(ctx, `UPDATE vods SET status = 'FAILED' WHERE channel_id = $1`, ch)
	require.NoError(t, err)
	_, err = r.CreateUpload(ctx, entity.Upload{ChannelID: ch}, entity.UploadLimits{Active: 3, Daily: 4})
	require.ErrorAs(t, err, &le)
	require.Equal(t, entity.UploadLimitDaily, le.Limit)
	require.Positive(t, le.RetryAfter)
}

func TestExpireVODsIgnoresUploads(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	ch := uuid.NewString()

	old := insertVOD(t, pg, ch, 60, time.Now().Add(-time.Minute))
	_, vodID := completed(t, r, ch)
	_, err := r.MarkVODReady(ctx, repo.UploadReadyInput{VODID: vodID, PlaylistPath: "videos/" + vodID + "/master.m3u8"})
	require.NoError(t, err)

	n, err := r.ExpireVODs(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "only the broadcast recording expires")

	_, err = r.GetVOD(ctx, old.ID)
	require.ErrorIs(t, err, entity.ErrNotFound)

	up, err := r.GetVOD(ctx, vodID)
	require.NoError(t, err)
	require.Nil(t, up.ExpiresAt)

	// Continue watching includes an uploaded video (expires_at NULL).
	user := uuid.NewString()
	require.NoError(t, r.SaveProgress(ctx, entity.WatchProgress{UserID: user, VODID: vodID, PositionSec: 30, UpdatedAt: time.Now()}, false))

	items, err := r.ContinueWatching(ctx, user, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, entity.SourceUpload, items[0].VOD.Source)

	stats, err := r.DashboardStats(ctx, ch, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Equal(t, 1, stats.VODReadyPublic)
}
