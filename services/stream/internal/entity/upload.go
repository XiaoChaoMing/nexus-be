package entity

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"
)

// UploadStatus is the state of an upload session: UPLOADING → COMPLETED | ABORTED | EXPIRED.
type UploadStatus string

// Upload statuses.
const (
	UploadUploading UploadStatus = "UPLOADING"
	UploadCompleted UploadStatus = "COMPLETED"
	UploadAborted   UploadStatus = "ABORTED"
	UploadExpired   UploadStatus = "EXPIRED"
)

// Which quota an upload hit (UploadLimitError.Limit).
const (
	UploadLimitActive = "ACTIVE"
	UploadLimitDaily  = "DAILY"
)

// UploadQuotaWindow is the period UploadLimits.Daily counts over.
const UploadQuotaWindow = 24 * time.Hour

// UploadPart is a part stored in the multipart upload.
type UploadPart struct {
	ETag string `json:"etag"`
	Size int64  `json:"size"`
}

// UploadPartRef is a part handed to CompleteMultipartUpload, in part order.
type UploadPartRef struct {
	Number int
	ETag   string
}

// Upload is a creator sending a video file in parts; the source object is ObjectKey in the uploads bucket.
type Upload struct {
	ID          string
	ChannelID   string
	UserID      string
	VODID       *string
	FileName    string
	ContentType string
	SizeBytes   int64
	PartSize    int64
	PartCount   int
	S3UploadID  string
	ObjectKey   string
	Title       string
	Description string
	CategoryID  *string
	IsMature    bool
	Visibility  string
	Status      UploadStatus
	// Parts maps the part number to what storage returned for it.
	Parts           map[int]UploadPart
	SourceDeletedAt *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ExpiresAt       time.Time
}

// UploadLimits are a channel's upload quotas: Active counts sessions UPLOADING plus uploaded videos PROCESSING,
// Daily counts the sessions created over UploadQuotaWindow.
type UploadLimits struct {
	Active int
	Daily  int
}

// UploadQuota is a channel's use of its quotas; OldestDaily is when the oldest session of the window was created.
type UploadQuota struct {
	Active      int
	Daily       int
	OldestDaily *time.Time
}

// _activeRetryAfter is what a client is told to wait when too many uploads are in flight: processing has no
// predictable end.
const _activeRetryAfter = time.Minute

// Check returns an *UploadLimitError when q has no room left under l at now.
func (q UploadQuota) Check(l UploadLimits, now time.Time) error {
	if q.Active >= l.Active {
		return &UploadLimitError{Limit: UploadLimitActive, RetryAfter: _activeRetryAfter}
	}

	if q.Daily >= l.Daily {
		wait := UploadQuotaWindow
		if q.OldestDaily != nil {
			wait = max(q.OldestDaily.Add(UploadQuotaWindow).Sub(now), time.Second)
		}

		return &UploadLimitError{Limit: UploadLimitDaily, RetryAfter: wait}
	}

	return nil
}

// genericContentType is what browsers send when they do not know a file's type; the extension decides then.
const genericContentType = "application/octet-stream"

// VideoFormatOK reports whether the file name has a supported extension (MP4, MOV, MKV, WebM) and the content type,
// when there is one, a matching video type. ffprobe in media-svc is the real check.
func VideoFormatOK(fileName, contentType string) bool {
	switch strings.ToLower(path.Ext(fileName)) {
	case ".mp4", ".mov", ".mkv", ".webm":
	default:
		return false
	}

	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "", genericContentType, "video/mp4", "video/quicktime", "video/x-matroska", "video/webm":
		return true
	}

	return false
}

// PartCount is how many parts of partSize a file of size bytes is sent in.
func PartCount(size, partSize int64) int {
	if size <= 0 || partSize <= 0 {
		return 0
	}

	return int((size + partSize - 1) / partSize)
}

// UploadSourceKey is the object key of an upload's source file.
func UploadSourceKey(uploadID string) string {
	return "sources/" + uploadID
}

// Receiving says whether the session still takes parts and can be completed at now.
func (u Upload) Receiving(now time.Time) error {
	switch u.Status {
	case UploadCompleted:
		return ErrInvalidState
	case UploadAborted, UploadExpired:
		return ErrUploadExpired
	case UploadUploading:
	}

	if !now.Before(u.ExpiresAt) {
		return ErrUploadExpired
	}

	return nil
}

// ExpectedPartSize returns the exact size part n must have: partSize for every part but the last.
func (u Upload) ExpectedPartSize(n int) (int64, error) {
	if n < 1 || n > u.PartCount {
		return 0, ErrInvalidPart
	}

	if n < u.PartCount {
		return u.PartSize, nil
	}

	return u.SizeBytes - int64(u.PartCount-1)*u.PartSize, nil
}

// CheckPart validates part n of size bytes: ErrInvalidPart, or an *InvalidPartError when only the size is wrong.
func (u Upload) CheckPart(n int, size int64) error {
	want, err := u.ExpectedPartSize(n)
	if err != nil {
		return err
	}

	if size != want {
		return &InvalidPartError{ExpectedBytes: want}
	}

	return nil
}

// UploadedParts lists the received part numbers in ascending order.
func (u Upload) UploadedParts() []int {
	out := make([]int, 0, len(u.Parts))
	for n := range u.Parts {
		out = append(out, n)
	}

	slices.Sort(out)

	return out
}

// MissingParts lists the part numbers not received yet, in ascending order.
func (u Upload) MissingParts() []int {
	out := []int{}

	for n := 1; n <= u.PartCount; n++ {
		if _, ok := u.Parts[n]; !ok {
			out = append(out, n)
		}
	}

	return out
}

// PartRefs returns every received part in order, for CompleteMultipartUpload.
func (u Upload) PartRefs() []UploadPartRef {
	out := make([]UploadPartRef, 0, len(u.Parts))
	for _, n := range u.UploadedParts() {
		out = append(out, UploadPartRef{Number: n, ETag: u.Parts[n].ETag})
	}

	return out
}

var (
	// ErrFileTooLarge: the declared file size is above the upload limit (FileTooLargeError carries it).
	ErrFileTooLarge = errors.New("file too large")
	// ErrUnsupportedFormat: the file extension or content type is not a supported video format.
	ErrUnsupportedFormat = errors.New("unsupported video format")
	// ErrUploadLimit: the channel has too many uploads in flight or created today (UploadLimitError).
	ErrUploadLimit = errors.New("upload limit reached")
	// ErrUploadExpired: the upload session expired or was aborted.
	ErrUploadExpired = errors.New("upload expired")
	// ErrPartsMissing: completing an upload that lacks parts (PartsMissingError lists them).
	ErrPartsMissing = errors.New("upload parts missing")
	// ErrInvalidPart: a part number outside 1…partCount or a part of the wrong size (InvalidPartError).
	ErrInvalidPart = errors.New("invalid upload part")
	// ErrStorageUnavailable: object storage failed, or uploads are turned off.
	ErrStorageUnavailable = errors.New("storage unavailable")
)

// UploadLimitError is ErrUploadLimit with the quota that was hit and how long to wait.
type UploadLimitError struct {
	Limit      string
	RetryAfter time.Duration
}

func (e *UploadLimitError) Error() string {
	return fmt.Sprintf("%v: %s", ErrUploadLimit, e.Limit)
}

// Unwrap makes errors.Is(err, ErrUploadLimit) hold.
func (e *UploadLimitError) Unwrap() error { return ErrUploadLimit }

// PartsMissingError is ErrPartsMissing with the missing part numbers.
type PartsMissingError struct {
	Missing []int
}

func (e *PartsMissingError) Error() string {
	return fmt.Sprintf("%v: %d part(s)", ErrPartsMissing, len(e.Missing))
}

// Unwrap makes errors.Is(err, ErrPartsMissing) hold.
func (e *PartsMissingError) Unwrap() error { return ErrPartsMissing }

// InvalidPartError is ErrInvalidPart for a part of the wrong size.
type InvalidPartError struct {
	ExpectedBytes int64
}

func (e *InvalidPartError) Error() string {
	return fmt.Sprintf("%v: expected %d bytes", ErrInvalidPart, e.ExpectedBytes)
}

// Unwrap makes errors.Is(err, ErrInvalidPart) hold.
func (e *InvalidPartError) Unwrap() error { return ErrInvalidPart }

// FileTooLargeError is ErrFileTooLarge with the limit.
type FileTooLargeError struct {
	MaxBytes int64
}

func (e *FileTooLargeError) Error() string {
	return fmt.Sprintf("%v: limit %d bytes", ErrFileTooLarge, e.MaxBytes)
}

// Unwrap makes errors.Is(err, ErrFileTooLarge) hold.
func (e *FileTooLargeError) Unwrap() error { return ErrFileTooLarge }
