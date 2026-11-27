package uploads_test

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase/uploads"
	"github.com/stretchr/testify/require"
)

var (
	errDB = errors.New("db down")
	errS3 = errors.New("minio down")
)

const (
	_partSize = 8 << 20
	_bucket   = "nxl-uploads"
	_vodID    = "0199d4a2-0000-7000-8000-0000000000a1"
)

// _now is the fixed clock; sessions expire an hour later.
var _now = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC) //nolint:gochecknoglobals // test clock

type fakeRepo struct {
	quota     entity.UploadQuota
	quotaErr  error
	createErr error
	upload    entity.Upload
	getErr    error
	// savedStatus replaces the status SavePart returns (a session aborted meanwhile).
	savedStatus entity.UploadStatus
	saved       map[int]entity.UploadPart
	completeErr error
	abortOK     bool
	retryErr    error
	outcome     repo.UploadOutcome
	failedOK    bool
	listed      []entity.Upload
	progress    []int
	readyIn     []repo.UploadReadyInput
	marked      []string
	created     []entity.Upload
}

func (f *fakeRepo) UploadQuota(context.Context, string) (entity.UploadQuota, error) {
	return f.quota, f.quotaErr
}

func (f *fakeRepo) CreateUpload(_ context.Context, u entity.Upload, _ entity.UploadLimits) (entity.Upload, error) {
	if f.createErr != nil {
		return entity.Upload{}, f.createErr
	}

	f.created = append(f.created, u)

	return u, nil
}

func (f *fakeRepo) GetUpload(context.Context, string, string) (entity.Upload, error) {
	return f.upload, f.getErr
}

func (f *fakeRepo) SavePart(_ context.Context, _ string, n int, p entity.UploadPart) (entity.Upload, error) {
	if f.saved == nil {
		f.saved = map[int]entity.UploadPart{}
	}

	f.saved[n] = p

	u := f.upload
	if f.savedStatus != "" {
		u.Status = f.savedStatus
	}

	return u, nil
}

func (f *fakeRepo) CompleteUpload(context.Context, string, string) (entity.Upload, error) {
	if f.completeErr != nil {
		return entity.Upload{}, f.completeErr
	}

	u := f.upload
	vod := _vodID
	u.Status, u.VODID = entity.UploadCompleted, &vod

	return u, nil
}

func (f *fakeRepo) AbortUpload(context.Context, string, string) (entity.Upload, bool, error) {
	return f.upload, f.abortOK, f.getErr
}

func (f *fakeRepo) ExpireUploads(context.Context, int) ([]entity.Upload, error) {
	return f.listed, f.getErr
}

func (f *fakeRepo) AbortChannelUploads(context.Context, string) ([]entity.Upload, error) {
	return f.listed, f.getErr
}

func (f *fakeRepo) SourcesToPurge(context.Context, int) ([]entity.Upload, error) {
	return f.listed, f.getErr
}

func (f *fakeRepo) MarkSourceDeleted(_ context.Context, id string) error {
	f.marked = append(f.marked, id)

	return nil
}

func (f *fakeRepo) UploadOfVOD(context.Context, string, string) (entity.Upload, error) {
	return f.upload, f.getErr
}

func (f *fakeRepo) RetryVOD(_ context.Context, _, vodID, _ string) (entity.VOD, error) {
	if f.retryErr != nil {
		return entity.VOD{}, f.retryErr
	}

	return entity.VOD{ID: vodID, Source: entity.SourceUpload, Status: entity.VODProcessing}, nil
}

func (f *fakeRepo) SetProgress(_ context.Context, _ string, percent int, _ string) error {
	f.progress = append(f.progress, percent)

	return f.quotaErr
}

func (f *fakeRepo) MarkVODReady(_ context.Context, in repo.UploadReadyInput) (repo.UploadOutcome, error) {
	f.readyIn = append(f.readyIn, in)

	return f.outcome, f.quotaErr
}

func (f *fakeRepo) MarkVODFailed(context.Context, string, string, bool) (bool, error) {
	return f.failedOK, f.quotaErr
}

type fakeStore struct {
	err      error
	exists   bool
	parts    []int
	aborted  []string
	removed  []string
	complete int
}

func (s *fakeStore) NewMultipart(context.Context, string, string) (string, error) {
	return "s3-id", s.err
}

func (s *fakeStore) PutPart(_ context.Context, _, _ string, n int, body io.Reader, _ int64) (string, error) {
	if s.err != nil {
		return "", s.err
	}

	if _, err := io.Copy(io.Discard, body); err != nil {
		return "", err
	}

	s.parts = append(s.parts, n)

	return "etag-" + strconv.Itoa(n), nil
}

func (s *fakeStore) CompleteMultipart(context.Context, string, string, []entity.UploadPartRef) error {
	s.complete++

	return s.err
}

func (s *fakeStore) AbortMultipart(_ context.Context, key, _ string) error {
	s.aborted = append(s.aborted, key)

	return s.err
}

func (s *fakeStore) Exists(context.Context, string) (bool, error) { return s.exists, s.err }

func (s *fakeStore) Remove(_ context.Context, key string) error {
	s.removed = append(s.removed, key)

	return s.err
}

func newUC(r *fakeRepo, s repo.ObjectStore) *uploads.UseCase {
	return uploads.New(r, s, uploads.Config{
		Bucket: _bucket, MaxBytes: 2 << 30, PartSize: _partSize, TTL: time.Hour,
		Limits: entity.UploadLimits{Active: 3, Daily: 10},
	}, logger.New("error")).WithClock(func() time.Time { return _now })
}

// session is an UPLOADING session of two parts (8 MiB + 100 bytes).
func session() entity.Upload {
	id := uuid.NewString()

	return entity.Upload{
		ID: id, ChannelID: "ch1", Status: entity.UploadUploading, SizeBytes: _partSize + 100, PartSize: _partSize,
		PartCount: 2, S3UploadID: "s3-id", ObjectKey: entity.UploadSourceKey(id), Parts: map[int]entity.UploadPart{},
		ExpiresAt: _now.Add(time.Hour),
	}
}

func withStatus(u entity.Upload, s entity.UploadStatus) entity.Upload {
	u.Status = s

	return u
}

var (
	_channel   = entity.Channel{ID: "ch1"}                  //nolint:gochecknoglobals // fixture
	_suspended = entity.Channel{ID: "ch1", Suspended: true} //nolint:gochecknoglobals // fixture
)

func newUpload(name, contentType string, size int64) usecase.NewUpload {
	return usecase.NewUpload{
		UserID: "u1", FileName: name, ContentType: contentType, SizeBytes: size, Title: "t", Visibility: entity.VisibilityPublic,
	}
}

func TestCreate(t *testing.T) { //nolint:funlen // one table of outcomes
	t.Parallel()

	oldest := _now.Add(-23 * time.Hour)

	for _, tc := range []struct {
		name    string
		ch      entity.Channel
		in      usecase.NewUpload
		repo    *fakeRepo
		store   *fakeStore
		want    error
		limit   string
		aborted bool
	}{
		{name: "mp4", ch: _channel, in: newUpload("Clip.MP4", "video/mp4", 300<<20), repo: &fakeRepo{}, store: &fakeStore{}},
		{name: "octet-stream mkv", ch: _channel, in: newUpload("a.mkv", "application/octet-stream", 1), repo: &fakeRepo{}, store: &fakeStore{}},
		{name: "suspended", ch: _suspended, in: newUpload("a.mp4", "", 1), repo: &fakeRepo{}, store: &fakeStore{}, want: entity.ErrChannelSuspended},
		{name: "bad extension", ch: _channel, in: newUpload("a.avi", "video/mp4", 1), repo: &fakeRepo{}, store: &fakeStore{}, want: entity.ErrUnsupportedFormat},
		{name: "bad type", ch: _channel, in: newUpload("a.mp4", "image/png", 1), repo: &fakeRepo{}, store: &fakeStore{}, want: entity.ErrUnsupportedFormat},
		{name: "empty file", ch: _channel, in: newUpload("a.mp4", "", 0), repo: &fakeRepo{}, store: &fakeStore{}, want: entity.ErrUnsupportedFormat},
		{name: "too large", ch: _channel, in: newUpload("a.mp4", "", 2<<30+1), repo: &fakeRepo{}, store: &fakeStore{}, want: entity.ErrFileTooLarge},
		{
			name: "active quota", ch: _channel, in: newUpload("a.mp4", "", 1), repo: &fakeRepo{quota: entity.UploadQuota{Active: 3}},
			store: &fakeStore{}, want: entity.ErrUploadLimit, limit: entity.UploadLimitActive,
		},
		{
			name: "daily quota", ch: _channel, in: newUpload("a.mp4", "", 1),
			repo:  &fakeRepo{quota: entity.UploadQuota{Daily: 10, OldestDaily: &oldest}},
			store: &fakeStore{}, want: entity.ErrUploadLimit, limit: entity.UploadLimitDaily,
		},
		{
			name: "quota lost under the lock", ch: _channel, in: newUpload("a.mp4", "", 1),
			repo:  &fakeRepo{createErr: &entity.UploadLimitError{Limit: entity.UploadLimitActive}},
			store: &fakeStore{}, want: entity.ErrUploadLimit, limit: entity.UploadLimitActive, aborted: true,
		},
		{name: "quota db error", ch: _channel, in: newUpload("a.mp4", "", 1), repo: &fakeRepo{quotaErr: errDB}, store: &fakeStore{}, want: errDB},
		{
			name: "insert fails", ch: _channel, in: newUpload("a.mp4", "", 1), repo: &fakeRepo{createErr: errDB},
			store: &fakeStore{}, want: errDB, aborted: true,
		},
		{
			name: "storage down", ch: _channel, in: newUpload("a.mp4", "", 1), repo: &fakeRepo{},
			store: &fakeStore{err: errS3}, want: entity.ErrStorageUnavailable,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			u, err := newUC(tc.repo, tc.store).Create(context.Background(), tc.ch, tc.in)
			require.Equal(t, tc.aborted, len(tc.store.aborted) == 1, "multipart aborted")

			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)

				if tc.limit != "" {
					var le *entity.UploadLimitError
					require.ErrorAs(t, err, &le)
					require.Equal(t, tc.limit, le.Limit)
				}

				return
			}

			require.NoError(t, err)
			require.Equal(t, entity.UploadUploading, u.Status)
			require.Equal(t, entity.PartCount(tc.in.SizeBytes, _partSize), u.PartCount)
			require.Equal(t, "s3-id", u.S3UploadID)
			require.Equal(t, "sources/"+u.ID, u.ObjectKey)
			require.Equal(t, _now.Add(time.Hour), u.ExpiresAt)
		})
	}
}

func TestCreateLimitDetails(t *testing.T) {
	t.Parallel()

	oldest := _now.Add(-23 * time.Hour)
	_, err := newUC(&fakeRepo{quota: entity.UploadQuota{Daily: 10, OldestDaily: &oldest}}, &fakeStore{}).
		Create(context.Background(), _channel, newUpload("a.mp4", "", 1))

	var le *entity.UploadLimitError
	require.ErrorAs(t, err, &le)
	require.Equal(t, time.Hour, le.RetryAfter, "the oldest session leaves the 24 h window in an hour")

	_, err = newUC(&fakeRepo{}, &fakeStore{}).Create(context.Background(), _channel, newUpload("a.mp4", "", 3<<30))

	var tl *entity.FileTooLargeError
	require.ErrorAs(t, err, &tl)
	require.Equal(t, int64(2<<30), tl.MaxBytes)
}

func TestPutPart(t *testing.T) { //nolint:funlen // one table of outcomes
	t.Parallel()

	expired := session()
	expired.ExpiresAt = _now

	for _, tc := range []struct {
		name     string
		ch       entity.Channel
		repo     *fakeRepo
		store    *fakeStore
		n        int
		size     int64
		want     error
		expected int64
	}{
		{name: "first part", ch: _channel, repo: &fakeRepo{upload: session()}, store: &fakeStore{}, n: 1, size: _partSize},
		{name: "last part", ch: _channel, repo: &fakeRepo{upload: session()}, store: &fakeStore{}, n: 2, size: 100},
		{name: "not found", ch: _channel, repo: &fakeRepo{getErr: entity.ErrNotFound}, store: &fakeStore{}, n: 1, size: 1, want: entity.ErrNotFound},
		{name: "db error", ch: _channel, repo: &fakeRepo{getErr: errDB}, store: &fakeStore{}, n: 1, size: 1, want: errDB},
		{name: "suspended", ch: _suspended, repo: &fakeRepo{upload: session()}, store: &fakeStore{}, n: 1, size: _partSize, want: entity.ErrChannelSuspended},
		{name: "past ttl", ch: _channel, repo: &fakeRepo{upload: expired}, store: &fakeStore{}, n: 1, size: _partSize, want: entity.ErrUploadExpired},
		{
			name: "aborted", ch: _channel, repo: &fakeRepo{upload: withStatus(session(), entity.UploadAborted)}, store: &fakeStore{},
			n: 1, size: _partSize, want: entity.ErrUploadExpired,
		},
		{
			name: "completed", ch: _channel, repo: &fakeRepo{upload: withStatus(session(), entity.UploadCompleted)}, store: &fakeStore{},
			n: 1, size: _partSize, want: entity.ErrInvalidState,
		},
		{name: "number out of range", ch: _channel, repo: &fakeRepo{upload: session()}, store: &fakeStore{}, n: 3, size: 100, want: entity.ErrInvalidPart},
		{name: "number zero", ch: _channel, repo: &fakeRepo{upload: session()}, store: &fakeStore{}, n: 0, size: 100, want: entity.ErrInvalidPart},
		{
			name: "short part", ch: _channel, repo: &fakeRepo{upload: session()}, store: &fakeStore{}, n: 1, size: 100,
			want: entity.ErrInvalidPart, expected: _partSize,
		},
		{
			name: "long last part", ch: _channel, repo: &fakeRepo{upload: session()}, store: &fakeStore{}, n: 2, size: _partSize,
			want: entity.ErrInvalidPart, expected: 100,
		},
		{
			name: "storage down", ch: _channel, repo: &fakeRepo{upload: session()}, store: &fakeStore{err: errS3}, n: 1, size: _partSize,
			want: entity.ErrStorageUnavailable,
		},
		{
			name: "aborted while sending", ch: _channel, repo: &fakeRepo{upload: session(), savedStatus: entity.UploadAborted},
			store: &fakeStore{}, n: 1, size: _partSize, want: entity.ErrUploadExpired,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body := strings.NewReader(strings.Repeat("x", int(min(tc.size, 1<<10))))
			err := newUC(tc.repo, tc.store).PutPart(context.Background(), tc.ch, "id", tc.n, body, tc.size)

			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)

				if tc.expected > 0 {
					var pe *entity.InvalidPartError
					require.ErrorAs(t, err, &pe)
					require.Equal(t, tc.expected, pe.ExpectedBytes)
				}

				return
			}

			require.NoError(t, err)
			require.Equal(t, []int{tc.n}, tc.store.parts)
			require.Equal(t, entity.UploadPart{ETag: "etag-" + strconv.Itoa(tc.n), Size: tc.size}, tc.repo.saved[tc.n])
		})
	}
}

func complete(u entity.Upload) entity.Upload {
	u.Parts = map[int]entity.UploadPart{1: {ETag: "a", Size: _partSize}, 2: {ETag: "b", Size: 100}}

	return u
}

func TestComplete(t *testing.T) {
	t.Parallel()

	half := session()
	half.Parts = map[int]entity.UploadPart{2: {ETag: "b", Size: 100}}
	done := withStatus(complete(session()), entity.UploadCompleted)
	vod := _vodID
	done.VODID = &vod

	for _, tc := range []struct {
		name      string
		ch        entity.Channel
		repo      *fakeRepo
		store     *fakeStore
		want      error
		completes int
	}{
		{name: "completed", ch: _channel, repo: &fakeRepo{upload: complete(session())}, store: &fakeStore{}, completes: 1},
		{name: "again returns the same vod", ch: _channel, repo: &fakeRepo{upload: done}, store: &fakeStore{}},
		{name: "parts missing", ch: _channel, repo: &fakeRepo{upload: half}, store: &fakeStore{}, want: entity.ErrPartsMissing},
		{name: "not found", ch: _channel, repo: &fakeRepo{getErr: entity.ErrNotFound}, store: &fakeStore{}, want: entity.ErrNotFound},
		{name: "suspended", ch: _suspended, repo: &fakeRepo{upload: complete(session())}, store: &fakeStore{}, want: entity.ErrChannelSuspended},
		{
			name: "expired", ch: _channel, repo: &fakeRepo{upload: withStatus(complete(session()), entity.UploadExpired)},
			store: &fakeStore{}, want: entity.ErrUploadExpired,
		},
		{
			name: "storage down", ch: _channel, repo: &fakeRepo{upload: complete(session())}, store: &fakeStore{err: errS3},
			want: entity.ErrStorageUnavailable, completes: 1,
		},
		{
			name: "aborted meanwhile", ch: _channel, repo: &fakeRepo{upload: complete(session()), completeErr: entity.ErrUploadExpired},
			store: &fakeStore{}, want: entity.ErrUploadExpired, completes: 1,
		},
		{
			name: "db error", ch: _channel, repo: &fakeRepo{upload: complete(session()), completeErr: errDB},
			store: &fakeStore{}, want: errDB, completes: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			u, err := newUC(tc.repo, tc.store).Complete(context.Background(), tc.ch, "id")
			require.Equal(t, tc.completes, tc.store.complete)

			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)

				return
			}

			require.NoError(t, err)
			require.Equal(t, entity.UploadCompleted, u.Status)
			require.Equal(t, _vodID, *u.VODID)
		})
	}

	_, err := newUC(&fakeRepo{upload: half}, &fakeStore{}).Complete(context.Background(), _channel, "id")

	var pm *entity.PartsMissingError
	require.ErrorAs(t, err, &pm)
	require.Equal(t, []int{1}, pm.Missing)
}

func TestAbort(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		repo    *fakeRepo
		want    error
		aborted int
	}{
		{name: "aborted", repo: &fakeRepo{upload: session(), abortOK: true}, aborted: 1},
		{name: "already aborted", repo: &fakeRepo{upload: withStatus(session(), entity.UploadAborted)}},
		{name: "expired", repo: &fakeRepo{upload: withStatus(session(), entity.UploadExpired)}},
		{name: "completed", repo: &fakeRepo{upload: withStatus(session(), entity.UploadCompleted)}, want: entity.ErrInvalidState},
		{name: "not found", repo: &fakeRepo{getErr: entity.ErrNotFound}, want: entity.ErrNotFound},
		{name: "db error", repo: &fakeRepo{getErr: errDB}, want: errDB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &fakeStore{}
			err := newUC(tc.repo, s).Abort(context.Background(), "ch1", "id")
			require.ErrorIs(t, err, tc.want)
			require.Len(t, s.aborted, tc.aborted)
		})
	}
}

func TestRetry(t *testing.T) {
	t.Parallel()

	gone := session()
	deletedAt := _now
	gone.SourceDeletedAt = &deletedAt

	for _, tc := range []struct {
		name  string
		ch    entity.Channel
		repo  *fakeRepo
		store repo.ObjectStore
		want  error
	}{
		{name: "retried", ch: _channel, repo: &fakeRepo{upload: session()}, store: &fakeStore{exists: true}},
		{name: "suspended", ch: _suspended, repo: &fakeRepo{upload: session()}, store: &fakeStore{exists: true}, want: entity.ErrChannelSuspended},
		{name: "source gone from storage", ch: _channel, repo: &fakeRepo{upload: session()}, store: &fakeStore{}, want: entity.ErrInvalidState},
		{name: "storage down", ch: _channel, repo: &fakeRepo{upload: session()}, store: &fakeStore{err: errS3}, want: entity.ErrStorageUnavailable},
		{name: "uploads turned off", ch: _channel, repo: &fakeRepo{upload: session()}, store: nil, want: entity.ErrStorageUnavailable},
		{
			name: "source deleted: the repo refuses", ch: _channel, repo: &fakeRepo{upload: gone, retryErr: entity.ErrInvalidState},
			store: &fakeStore{}, want: entity.ErrInvalidState,
		},
		{
			name: "broadcast vod", ch: _channel, repo: &fakeRepo{getErr: entity.ErrNotFound, retryErr: entity.ErrInvalidState},
			store: &fakeStore{}, want: entity.ErrInvalidState,
		},
		{
			name: "unknown vod", ch: _channel, repo: &fakeRepo{getErr: entity.ErrNotFound, retryErr: entity.ErrNotFound},
			store: &fakeStore{}, want: entity.ErrNotFound,
		},
		{name: "db error", ch: _channel, repo: &fakeRepo{getErr: errDB}, store: &fakeStore{}, want: errDB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v, err := newUC(tc.repo, tc.store).Retry(context.Background(), tc.ch, _vodID)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)

				return
			}

			require.NoError(t, err)
			require.Equal(t, entity.VODProcessing, v.Status)
		})
	}
}

func TestDisabledUploads(t *testing.T) {
	t.Parallel()

	r := &fakeRepo{listed: []entity.Upload{session()}}
	uc := newUC(r, nil)

	_, err := uc.Create(context.Background(), _channel, newUpload("a.mp4", "", 1))
	require.ErrorIs(t, err, entity.ErrStorageUnavailable)

	n, err := uc.PurgeSources(context.Background())
	require.NoError(t, err)
	require.Zero(t, n, "nothing to purge without storage")

	n, err = uc.ExpireStale(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n, "sessions still expire")
}

func TestProcessed(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		outcome repo.UploadOutcome
		dbErr   error
		removed int
	}{
		{name: "ready deletes the source", outcome: repo.UploadOutcome{Result: repo.UploadResultReady, Upload: session()}, removed: 1},
		{name: "deleted while processing", outcome: repo.UploadOutcome{Result: repo.UploadResultDeleted, Upload: session()}, removed: 1},
		{name: "redelivery is ignored", outcome: repo.UploadOutcome{Result: repo.UploadResultIgnored}},
		{name: "db error is retried", dbErr: errDB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r, s := &fakeRepo{outcome: tc.outcome, quotaErr: tc.dbErr}, &fakeStore{}
			err := newUC(r, s).Processed(context.Background(), events.UploadProcessed{
				VODID: _vodID, DurationSec: 12.6, PlaylistPath: "videos/" + _vodID + "/master.m3u8",
			})
			require.ErrorIs(t, err, tc.dbErr)
			require.Len(t, s.removed, tc.removed)
			require.Len(t, r.marked, tc.removed)
			require.Equal(t, 13, r.readyIn[0].DurationSec, "rounded")
		})
	}

	// A source that cannot be removed is left for PurgeSources.
	r, s := &fakeRepo{outcome: repo.UploadOutcome{Result: repo.UploadResultReady, Upload: session()}}, &fakeStore{err: errS3}
	require.NoError(t, newUC(r, s).Processed(context.Background(), events.UploadProcessed{VODID: _vodID}))
	require.Empty(t, r.marked)
}

func TestFailedProgressAndChannel(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	r, s := &fakeRepo{failedOK: true, listed: []entity.Upload{session(), session()}}, &fakeStore{}
	uc := newUC(r, s)

	require.NoError(t, uc.Failed(ctx, events.UploadFailed{VODID: _vodID, Reason: events.UploadFailNoVideoStream}))
	require.NoError(t, uc.Progress(ctx, events.UploadProgress{VODID: _vodID, Percent: 42, Stage: events.UploadStageTranscoding}))
	require.Equal(t, []int{42}, r.progress)

	require.NoError(t, uc.AbortChannel(ctx, "ch1"))
	require.Len(t, s.aborted, 2)

	n, err := uc.ExpireStale(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.Len(t, s.aborted, 4)

	n, err = uc.PurgeSources(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, n)

	bad := newUC(&fakeRepo{quotaErr: errDB, getErr: errDB}, s)
	require.ErrorIs(t, bad.Failed(ctx, events.UploadFailed{VODID: _vodID}), errDB)
	require.ErrorIs(t, bad.Progress(ctx, events.UploadProgress{VODID: _vodID}), errDB)
	require.ErrorIs(t, bad.AbortChannel(ctx, "ch1"), errDB)
	_, err = bad.ExpireStale(ctx)
	require.ErrorIs(t, err, errDB)
}
