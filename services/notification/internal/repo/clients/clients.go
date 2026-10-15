// Package clients reads the internal snapshot APIs of identity and channel-svc (X-Internal-Secret) for the
// one-off backfill of notification-svc's copies; they are never called while handling an event.
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

	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/repo"
)

const (
	_secretHeader    = "X-Internal-Secret" //nolint:gosec // a header name, not a secret
	_snapshotTimeout = 30 * time.Second
)

// ErrStatus is an answer other than 200.
var ErrStatus = errors.New("unexpected status")

type caller struct {
	base   string
	secret string
	http   *http.Client
}

func newCaller(base, secret string) caller {
	return caller{base: strings.TrimSuffix(base, "/"), secret: secret, http: &http.Client{Timeout: _snapshotTimeout}}
}

type page[T any] struct {
	Items     []T     `json:"items"`
	NextAfter *string `json:"nextAfter"`
}

func fetch[T any](ctx context.Context, c caller, path, after string, limit int) (items []T, next string, err error) {
	q := url.Values{"after": {after}, "limit": {strconv.Itoa(limit)}}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path+"?"+q.Encode(), http.NoBody)
	if err != nil {
		return nil, "", fmt.Errorf("snapshot request: %w", err)
	}

	req.Header.Set(_secretHeader, c.secret)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("GET %s: %w %d", path, ErrStatus, resp.StatusCode)
	}

	var p page[T]
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return nil, "", fmt.Errorf("GET %s - decode: %w", path, err)
	}

	if p.NextAfter != nil {
		next = *p.NextAfter
	}

	return p.Items, next, nil
}

// Snapshots implements repo.SnapshotClient.
type Snapshots struct {
	identity, channel caller
}

var _ repo.SnapshotClient = (*Snapshots)(nil)

// NewSnapshots -.
func NewSnapshots(identityURL, channelURL, secret string) *Snapshots {
	return &Snapshots{identity: newCaller(identityURL, secret), channel: newCaller(channelURL, secret)}
}

type userRow struct {
	ID            string     `json:"id"`
	Username      string     `json:"username"`
	DisplayName   string     `json:"displayName"`
	Email         string     `json:"email"`
	EmailVerified bool       `json:"emailVerified"`
	Locale        string     `json:"locale"`
	Timezone      string     `json:"timezone"`
	Status        string     `json:"status"`
	LockedUntil   *time.Time `json:"lockedUntil"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

// Users -.
func (s *Snapshots) Users(ctx context.Context, after string, limit int) ([]repo.Snapshot[entity.UserCopy], string, error) {
	rows, next, err := fetch[userRow](ctx, s.identity, "/internal/v1/users", after, limit)
	if err != nil {
		return nil, "", err
	}

	out := make([]repo.Snapshot[entity.UserCopy], 0, len(rows))
	for _, u := range rows {
		out = append(out, repo.Snapshot[entity.UserCopy]{UpdatedAt: u.UpdatedAt, Row: entity.UserCopy{
			ID: u.ID, Username: &u.Username, DisplayName: &u.DisplayName, Email: &u.Email, EmailVerified: &u.EmailVerified,
			Locale: &u.Locale, Timezone: &u.Timezone, Status: &u.Status, LockedUntil: u.LockedUntil,
		}})
	}

	return out, next, nil
}

type blockRow struct {
	BlockerID string `json:"blockerId"`
	BlockedID string `json:"blockedId"`
}

// Blocks -.
func (s *Snapshots) Blocks(ctx context.Context, after string, limit int) (pairs [][2]string, next string, err error) {
	rows, next, err := fetch[blockRow](ctx, s.identity, "/internal/v1/user-blocks", after, limit)
	if err != nil {
		return nil, "", err
	}

	pairs = make([][2]string, 0, len(rows))
	for _, b := range rows {
		pairs = append(pairs, [2]string{b.BlockerID, b.BlockedID})
	}

	return pairs, next, nil
}

type channelRow struct {
	ID          string    `json:"id"`
	OwnerID     string    `json:"ownerId"`
	Handle      string    `json:"handle"`
	DisplayName string    `json:"displayName"`
	AvatarURL   *string   `json:"avatarUrl"`
	Status      string    `json:"status"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Channels -.
func (s *Snapshots) Channels(ctx context.Context, after string, limit int) ([]repo.Snapshot[entity.ChannelPatch], string, error) {
	rows, next, err := fetch[channelRow](ctx, s.channel, "/internal/v1/channels", after, limit)
	if err != nil {
		return nil, "", err
	}

	out := make([]repo.Snapshot[entity.ChannelPatch], 0, len(rows))
	for _, c := range rows {
		out = append(out, repo.Snapshot[entity.ChannelPatch]{UpdatedAt: c.UpdatedAt, Row: entity.ChannelPatch{
			ID: c.ID, OwnerID: &c.OwnerID, Handle: &c.Handle, DisplayName: &c.DisplayName, AvatarURL: c.AvatarURL,
			SetAvatar: true, Status: &c.Status,
		}})
	}

	return out, next, nil
}

type followRow struct {
	UserID      string    `json:"userId"`
	ChannelID   string    `json:"channelId"`
	NotifyLevel string    `json:"notifyLevel"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Follows -.
func (s *Snapshots) Follows(ctx context.Context, after string, limit int) ([]entity.Follow, string, error) {
	rows, next, err := fetch[followRow](ctx, s.channel, "/internal/v1/follows", after, limit)
	if err != nil {
		return nil, "", err
	}

	out := make([]entity.Follow, 0, len(rows))
	for _, f := range rows {
		out = append(out, entity.Follow(f))
	}

	return out, next, nil
}
