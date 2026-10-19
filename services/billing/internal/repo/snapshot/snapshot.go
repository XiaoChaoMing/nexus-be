// Package snapshot reads channel-svc's internal channel snapshot (X-Internal-Secret) for the one-off
// `billing backfill-channels`.
package snapshot

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

	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo"
)

const (
	_secretHeader = "X-Internal-Secret" //nolint:gosec // a header name, not a secret
	_timeout      = 30 * time.Second
)

// ErrStatus is an answer other than 200.
var ErrStatus = errors.New("unexpected status")

// Channels implements repo.ChannelSnapshot.
type Channels struct {
	base, secret string
	http         *http.Client
}

var _ repo.ChannelSnapshot = (*Channels)(nil)

// New -.
func New(base, secret string) *Channels {
	return &Channels{base: strings.TrimSuffix(base, "/"), secret: secret, http: &http.Client{Timeout: _timeout}}
}

type row struct {
	ID          string    `json:"id"`
	OwnerID     string    `json:"ownerId"`
	Handle      string    `json:"handle"`
	DisplayName string    `json:"displayName"`
	AvatarURL   *string   `json:"avatarUrl"`
	Status      string    `json:"status"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Channels returns one page after the given id and the next cursor ("" at the end).
func (c *Channels) Channels(ctx context.Context, after string, limit int) ([]repo.SnapshotChannel, string, error) {
	q := url.Values{"after": {after}, "limit": {strconv.Itoa(limit)}}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/internal/v1/channels?"+q.Encode(), http.NoBody)
	if err != nil {
		return nil, "", fmt.Errorf("snapshot - request: %w", err)
	}

	req.Header.Set(_secretHeader, c.secret)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("snapshot - channels: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("snapshot - channels: %w %d", ErrStatus, resp.StatusCode)
	}

	var page struct {
		Items     []row   `json:"items"`
		NextAfter *string `json:"nextAfter"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, "", fmt.Errorf("snapshot - decode: %w", err)
	}

	out := make([]repo.SnapshotChannel, 0, len(page.Items))
	for _, r := range page.Items {
		status := entity.ChannelActive
		if r.Status == entity.ChannelSuspended {
			status = entity.ChannelSuspended
		}

		out = append(out, repo.SnapshotChannel{UpdatedAt: r.UpdatedAt, Channel: entity.Channel{
			ID: r.ID, OwnerID: r.OwnerID, Handle: r.Handle, DisplayName: r.DisplayName, AvatarURL: r.AvatarURL, Status: status,
		}})
	}

	next := ""
	if page.NextAfter != nil {
		next = *page.NextAfter
	}

	return out, next, nil
}
