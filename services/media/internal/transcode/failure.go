package transcode

import (
	"errors"

	ev "github.com/minhnn/nexuslive-be/pkg/events"
)

// Failures of one job. Only ErrStorage is transient: the job is tried again in-process, and the
// upload.failed.v1 it ends with is retryable.
var (
	ErrUnsupportedFormat = errors.New("transcode: unsupported format")
	ErrNoVideoStream     = errors.New("transcode: no video stream")
	ErrDurationTooLong   = errors.New("transcode: duration too long")
	ErrFileCorrupt       = errors.New("transcode: file corrupt")
	ErrTranscodeFailed   = errors.New("transcode: ffmpeg failed")
	// ErrTimeLimit: the job ran past 3 × duration + 10 min (usually because live streams took the CPU).
	ErrTimeLimit = errors.New("transcode: time limit exceeded")
	ErrStorage   = errors.New("transcode: storage unavailable")
	// ErrSourceMissing: the source object is gone, so trying again cannot help.
	ErrSourceMissing = errors.New("transcode: source object missing")
	// ErrBadSource: the event names a bucket or key media-svc must not read.
	ErrBadSource = errors.New("transcode: source not allowed")
)

// Failure is the reason and retry flag published in upload.failed.v1.
type Failure struct {
	Reason    string
	Retryable bool
}

// Classify maps a job error to its upload.failed.v1 reason; anything unknown is a non-retryable
// TRANSCODE_FAILED.
func Classify(err error) Failure {
	switch {
	case errors.Is(err, ErrUnsupportedFormat):
		return Failure{Reason: ev.UploadFailUnsupportedFormat}
	case errors.Is(err, ErrNoVideoStream):
		return Failure{Reason: ev.UploadFailNoVideoStream}
	case errors.Is(err, ErrDurationTooLong):
		return Failure{Reason: ev.UploadFailDurationTooLong}
	case errors.Is(err, ErrFileCorrupt):
		return Failure{Reason: ev.UploadFailFileCorrupt}
	case errors.Is(err, ErrTimeLimit):
		return Failure{Reason: ev.UploadFailTranscodeFailed, Retryable: true}
	case errors.Is(err, ErrStorage):
		return Failure{Reason: ev.UploadFailStorageError, Retryable: true}
	case errors.Is(err, ErrSourceMissing), errors.Is(err, ErrBadSource):
		return Failure{Reason: ev.UploadFailStorageError}
	default:
		return Failure{Reason: ev.UploadFailTranscodeFailed}
	}
}

// transient reports whether the same job may succeed when tried again right away.
func transient(err error) bool {
	return errors.Is(err, ErrStorage)
}
