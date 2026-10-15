// Package safety implements reports, the admin queue and actions, the audit log and the copies of safety-svc.
package safety

import (
	"encoding/base64"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/safety/internal/entity"
	"github.com/minhnn/nexuslive-be/services/safety/internal/repo"
	"github.com/minhnn/nexuslive-be/services/safety/internal/usecase"
)

const (
	_defaultRateLimit = 10
	_rateWindow       = time.Hour
	_defaultEvidence  = 300 * time.Millisecond
	_defaultLimit     = 20
	_maxLimit         = 100
	_relatedMax       = 20
	_queryMax         = 50
	_backfillPage     = 500
	_cursorSep        = "|"
)

// Repos are the stores and clients the use case needs.
type Repos struct {
	Reports  repo.ReportRepo
	Actions  repo.ActionRepo
	Audit    repo.AuditRepo
	Replica  repo.ReplicaRepo
	Rate     repo.RateLimiter
	Evidence repo.EvidenceClient
	Snapshot repo.SnapshotClient
}

// UseCase implements every safety-svc use case.
type UseCase struct {
	r               Repos
	l               logger.Interface
	obs             usecase.Observer
	now             func() time.Time
	rateLimit       int
	evidenceTimeout time.Duration
}

var (
	_ usecase.Reports      = (*UseCase)(nil)
	_ usecase.AdminReports = (*UseCase)(nil)
	_ usecase.AdminActions = (*UseCase)(nil)
	_ usecase.Directory    = (*UseCase)(nil)
	_ usecase.Audit        = AuditLog{}
	_ usecase.Replica      = (*UseCase)(nil)
)

// Option -.
type Option func(*UseCase)

// RateLimit sets REPORT_RATE_LIMIT (reports per user and hour).
func RateLimit(n int) Option { return func(uc *UseCase) { uc.rateLimit = n } }

// EvidenceTimeout bounds the call to chat-svc (EVIDENCE_TIMEOUT).
func EvidenceTimeout(d time.Duration) Option { return func(uc *UseCase) { uc.evidenceTimeout = d } }

// WithObserver -.
func WithObserver(o usecase.Observer) Option { return func(uc *UseCase) { uc.obs = o } }

// Clock replaces time.Now (tests).
func Clock(now func() time.Time) Option { return func(uc *UseCase) { uc.now = now } }

// New -.
func New(r Repos, l logger.Interface, opts ...Option) *UseCase {
	uc := &UseCase{r: r, l: l, obs: nopObserver{}, now: time.Now, rateLimit: _defaultRateLimit, evidenceTimeout: _defaultEvidence}
	for _, o := range opts {
		o(uc)
	}

	return uc
}

type nopObserver struct{}

func (nopObserver) ReportFiled(string, string) {}

func limitOf(n int) int {
	switch {
	case n <= 0:
		return _defaultLimit
	case n > _maxLimit:
		return _maxLimit
	}

	return n
}

// trimmed returns the trimmed text and whether its length is within [lo, hi] runes.
func trimmed(s string, lo, hi int) (string, bool) {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)

	return s, n >= lo && n <= hi
}

func isUUID(s string) bool { return uuid.Validate(s) == nil }

func encodeCursor(parts ...string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join(parts, _cursorSep)))
}

func decodeCursor(c string) (head, id string, ok bool) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", "", false
	}

	head, id, ok = strings.Cut(string(b), _cursorSep)
	if !ok || !isUUID(id) {
		return "", "", false
	}

	return head, id, true
}

func timeCursor(c string) (*repo.TimeCursor, error) {
	if c == "" {
		return nil, nil //nolint:nilnil // first page
	}

	head, id, ok := decodeCursor(c)

	at, err := time.Parse(time.RFC3339Nano, head)
	if !ok || err != nil {
		return nil, entity.Invalid("cursor")
	}

	return &repo.TimeCursor{At: at, ID: id}, nil
}

func nameCursor(c string) (*repo.NameCursor, error) {
	if c == "" {
		return nil, nil //nolint:nilnil // first page
	}

	name, id, ok := decodeCursor(c)
	if !ok {
		return nil, entity.Invalid("cursor")
	}

	return &repo.NameCursor{Name: name, ID: id}, nil
}

// page cuts rows fetched with limit+1 and builds the next cursor from the last kept row.
func page[T any](rows []T, limit int, cursor func(T) string) usecase.Page[T] {
	if len(rows) <= limit {
		return usecase.Page[T]{Items: rows}
	}

	rows = rows[:limit]

	return usecase.Page[T]{Items: rows, Next: cursor(rows[len(rows)-1])}
}
