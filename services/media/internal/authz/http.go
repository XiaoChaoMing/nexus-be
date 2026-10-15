package authz

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/minhnn/nexuslive-be/services/media/internal/entity"
)

const (
	codeRecentlyEnded = "RECENTLY_ENDED"
	codeSuspended     = "CHANNEL_SUSPENDED"
)

// HTTP asks stream-svc (POST /internal/v1/ingest/authorize). Any failure rejects the publish:
// letting an unverified key through is worse than making OBS retry.
type HTTP struct {
	url    string
	secret string
	client *http.Client
}

// NewHTTP -.
func NewHTTP(baseURL, secret string) *HTTP {
	return &HTTP{
		url:    strings.TrimSuffix(baseURL, "/") + "/internal/v1/ingest/authorize",
		secret: secret,
		client: &http.Client{},
	}
}

type authorizeResponse struct {
	StreamID  string `json:"streamId"`
	ChannelID string `json:"channelId"`
	Resumed   bool   `json:"resumed"`
}

var (
	// ErrUnexpectedStatus -.
	ErrUnexpectedStatus = errors.New("authz - unexpected stream-svc status")
	// ErrEmptyGrant -.
	ErrEmptyGrant = errors.New("authz - stream-svc returned an empty grant")
)

// Authorize -.
func (a *HTTP) Authorize(ctx context.Context, streamKey, clientIP string) (Grant, error) {
	body, err := json.Marshal(map[string]string{"streamKey": streamKey, "clientIp": clientIP})
	if err != nil {
		return Grant{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url, bytes.NewReader(body))
	if err != nil {
		return Grant{}, fmt.Errorf("authz - request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Secret", a.secret)

	resp, err := a.client.Do(req)
	if err != nil {
		return Grant{}, fmt.Errorf("authz - stream-svc unreachable: %w", err)
	}
	defer resp.Body.Close()

	if err := statusError(resp); err != nil {
		return Grant{}, err
	}

	var out authorizeResponse
	if err = json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Grant{}, fmt.Errorf("authz - decode: %w", err)
	}

	if out.StreamID == "" || out.ChannelID == "" {
		return Grant{}, ErrEmptyGrant
	}

	return Grant(out), nil
}

// statusError maps a non-200 authorize answer to the sentinel media-svc logs and counts.
func statusError(resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusForbidden:
		if errorCode(resp) == codeSuspended {
			return entity.ErrSuspended
		}

		return entity.ErrUnauthorized
	case http.StatusConflict:
		return conflictError(resp)
	case http.StatusTooManyRequests:
		return entity.ErrCapacity
	default:
		return fmt.Errorf("%w %d", ErrUnexpectedStatus, resp.StatusCode)
	}
}

// conflictError tells "already live" from the cooldown after the creator ended a stream.
func conflictError(resp *http.Response) error {
	if errorCode(resp) == codeRecentlyEnded {
		return entity.ErrRecentlyEnded
	}

	return entity.ErrAlreadyLive
}

// errorCode reads error.code of a stream-svc error body ("" when unreadable).
func errorCode(resp *http.Response) string {
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}

	if json.NewDecoder(resp.Body).Decode(&body) != nil {
		return ""
	}

	return body.Error.Code
}
