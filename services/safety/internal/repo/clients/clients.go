// Package clients calls the internal APIs of other services (X-Internal-Secret): chat-svc for report
// evidence, identity / channel / stream for the one-off backfill of safety-svc's copies.
package clients

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/minhnn/nexuslive-be/services/safety/internal/entity"
	"github.com/minhnn/nexuslive-be/services/safety/internal/repo"
)

const (
	_secretHeader    = "X-Internal-Secret" //nolint:gosec // a header name, not a secret
	_snapshotTimeout = 30 * time.Second
)

// ErrStatus is an answer other than 200 / 404.
var ErrStatus = errors.New("unexpected status")

type caller struct {
	base   string
	secret string
	http   *http.Client
}

func newCaller(base, secret string, timeout time.Duration) caller {
	return caller{base: strings.TrimSuffix(base, "/"), secret: secret, http: &http.Client{Timeout: timeout}}
}

// get decodes the JSON answer of GET path?q into out; 404 → entity.ErrNotFound.
func (c caller) get(ctx context.Context, path string, q url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path+"?"+q.Encode(), http.NoBody)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}

	req.Header.Set(_secretHeader, c.secret)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return entity.ErrNotFound
	default:
		return fmt.Errorf("GET %s: %w %d", path, ErrStatus, resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("GET %s - decode: %w", path, err)
	}

	return nil
}

// Chat implements repo.EvidenceClient; the caller's context carries EVIDENCE_TIMEOUT.
type Chat struct{ caller }

var _ repo.EvidenceClient = (*Chat)(nil)

// NewChat -.
func NewChat(base, secret string, timeout time.Duration) *Chat {
	return &Chat{newCaller(base, secret, timeout)}
}

// Evidence returns, oldest first, the message and up to limit-1 earlier messages of its author (messageID
// set) or the last limit messages of a user (userID set).
func (c *Chat) Evidence(ctx context.Context, streamID, messageID, userID string, limit int) ([]entity.EvidenceItem, error) {
	var page struct {
		Items []entity.EvidenceItem `json:"items"`
	}

	q := url.Values{"streamId": {streamID}, "limit": {strconv.Itoa(limit)}}
	if messageID != "" {
		q.Set("messageId", messageID)
	} else {
		q.Set("userId", userID)
	}

	if err := c.get(ctx, "/internal/v1/chat/evidence", q, &page); err != nil {
		return nil, fmt.Errorf("chat evidence: %w", err)
	}

	return page.Items, nil
}

// Snapshots implements repo.SnapshotClient.
type Snapshots struct {
	identity, channel, stream caller
}

var _ repo.SnapshotClient = (*Snapshots)(nil)

// NewSnapshots -.
func NewSnapshots(identityURL, channelURL, streamURL, secret string) *Snapshots {
	return &Snapshots{
		identity: newCaller(identityURL, secret, _snapshotTimeout),
		channel:  newCaller(channelURL, secret, _snapshotTimeout),
		stream:   newCaller(streamURL, secret, _snapshotTimeout),
	}
}

type page[T any] struct {
	Items     []T     `json:"items"`
	NextAfter *string `json:"nextAfter"`
}

func fetch[T, R any](ctx context.Context, c caller, path, after string, limit int, conv func(T) repo.Snapshot[R],
) ([]repo.Snapshot[R], string, error) {
	var p page[T]

	q := url.Values{"after": {after}, "limit": {strconv.Itoa(limit)}}
	if err := c.get(ctx, path, q, &p); err != nil {
		return nil, "", fmt.Errorf("snapshot: %w", err)
	}

	out := make([]repo.Snapshot[R], 0, len(p.Items))
	for _, it := range p.Items {
		out = append(out, conv(it))
	}

	next := ""
	if p.NextAfter != nil {
		next = *p.NextAfter
	}

	return out, next, nil
}

type userRow struct {
	ID          string     `json:"id"`
	Username    string     `json:"username"`
	DisplayName string     `json:"displayName"`
	GlobalRole  string     `json:"globalRole"`
	Status      string     `json:"status"`
	LockedUntil *time.Time `json:"lockedUntil"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// Users -.
func (s *Snapshots) Users(ctx context.Context, after string, limit int) ([]repo.Snapshot[repo.UserPatch], string, error) {
	return fetch(ctx, s.identity, "/internal/v1/users", after, limit, func(u userRow) repo.Snapshot[repo.UserPatch] {
		return repo.Snapshot[repo.UserPatch]{UpdatedAt: u.UpdatedAt, Row: repo.UserPatch{
			ID: u.ID, Username: &u.Username, DisplayName: &u.DisplayName, GlobalRole: &u.GlobalRole, Status: &u.Status,
			LockedUntil: u.LockedUntil, CreatedAt: &u.CreatedAt,
		}}
	})
}

type channelRow struct {
	ID          string     `json:"id"`
	OwnerID     string     `json:"ownerId"`
	Handle      string     `json:"handle"`
	DisplayName string     `json:"displayName"`
	AvatarURL   *string    `json:"avatarUrl"`
	Status      string     `json:"status"`
	SuspendedAt *time.Time `json:"suspendedAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// Channels -.
func (s *Snapshots) Channels(ctx context.Context, after string, limit int) ([]repo.Snapshot[repo.ChannelPatch], string, error) {
	return fetch(ctx, s.channel, "/internal/v1/channels", after, limit, func(c channelRow) repo.Snapshot[repo.ChannelPatch] {
		return repo.Snapshot[repo.ChannelPatch]{UpdatedAt: c.UpdatedAt, Row: repo.ChannelPatch{
			ID: c.ID, OwnerID: &c.OwnerID, Handle: &c.Handle, DisplayName: &c.DisplayName, AvatarURL: c.AvatarURL,
			SetAvatar: true, Status: &c.Status, SuspendedAt: c.SuspendedAt,
		}}
	})
}

type streamRow struct {
	ID         string     `json:"id"`
	ChannelID  string     `json:"channelId"`
	Title      string     `json:"title"`
	Status     string     `json:"status"`
	EndReason  *string    `json:"endReason"`
	VODID      *string    `json:"vodId"`
	VODDeleted bool       `json:"vodDeleted"`
	StartedAt  *time.Time `json:"startedAt"`
	EndedAt    *time.Time `json:"endedAt"`
}

// Streams -; the stream snapshot has no updated_at, so rows are stamped with the time of the call.
func (s *Snapshots) Streams(ctx context.Context, after string, limit int) ([]repo.Snapshot[repo.StreamPatch], string, error) {
	now := time.Now()

	return fetch(ctx, s.stream, "/internal/v1/streams", after, limit, func(r streamRow) repo.Snapshot[repo.StreamPatch] {
		return repo.Snapshot[repo.StreamPatch]{UpdatedAt: now, Row: repo.StreamPatch{
			ID: r.ID, ChannelID: r.ChannelID, Title: &r.Title, Status: &r.Status, EndReason: r.EndReason, VODID: r.VODID,
			VODDeleted: r.VODDeleted, StartedAt: r.StartedAt, EndedAt: r.EndedAt,
		}}
	})
}
