// Package snapshot reads channel-svc's channel snapshot (GET /internal/v1/channels) to backfill chat-svc's
// copy once: JetStream keeps events for 7 days only.
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

	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo"
)

const (
	_secretHeader = "X-Internal-Secret" //nolint:gosec // a header name, not a secret
	_timeout      = 10 * time.Second
)

// ErrStatus is an answer other than 200.
var ErrStatus = errors.New("snapshot - unexpected status")

// Client implements repo.ChannelSnapshot.
type Client struct {
	base   string
	secret string
	http   *http.Client
}

var _ repo.ChannelSnapshot = (*Client)(nil)

// New -.
func New(baseURL, secret string) *Client {
	return &Client{base: strings.TrimSuffix(baseURL, "/"), secret: secret, http: &http.Client{Timeout: _timeout}}
}

type channelPage struct {
	Items []struct {
		ID          string  `json:"id"`
		OwnerID     string  `json:"ownerId"`
		Handle      string  `json:"handle"`
		DisplayName string  `json:"displayName"`
		AvatarURL   *string `json:"avatarUrl"`
		Status      string  `json:"status"`
	} `json:"items"`
	NextAfter *string `json:"nextAfter"`
}

// Channels -.
func (c *Client) Channels(ctx context.Context, after string, limit int) ([]entity.ChannelInfo, string, error) {
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
		return nil, "", fmt.Errorf("%w %d", ErrStatus, resp.StatusCode)
	}

	var page channelPage
	if err = json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, "", fmt.Errorf("snapshot - decode: %w", err)
	}

	out := make([]entity.ChannelInfo, 0, len(page.Items))
	for _, it := range page.Items {
		out = append(out, entity.ChannelInfo{
			ID: it.ID, OwnerID: it.OwnerID, Handle: it.Handle, DisplayName: it.DisplayName, AvatarURL: it.AvatarURL,
			Status: entity.ChannelStatus(it.Status),
		})
	}

	next := ""
	if page.NextAfter != nil {
		next = *page.NextAfter
	}

	return out, next, nil
}
