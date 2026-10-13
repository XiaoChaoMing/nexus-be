// Package authz decides whether a stream key may publish.
package authz

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"

	"github.com/minhnn/nexuslive-be/services/media/internal/entity"
)

// Grant is what a valid key resolves to.
type Grant struct {
	// StreamID is assigned by stream-svc from L2; empty in L1, the session manager then picks one.
	StreamID  string
	ChannelID string
	Resumed   bool
}

// Authorizer -.
type Authorizer interface {
	Authorize(ctx context.Context, streamKey, clientIP string) (Grant, error)
}

// StaticChannelID is the single channel served in phase L1.
const StaticChannelID = "dev-channel"

// Static accepts exactly one configured key (phase L1 only).
type Static struct {
	sum [32]byte
	set bool
}

// NewStatic -.
func NewStatic(key string) *Static {
	return &Static{sum: sha256.Sum256([]byte(key)), set: key != ""}
}

// Authorize compares hashes in constant time so the key length does not leak either.
func (s *Static) Authorize(_ context.Context, streamKey, _ string) (Grant, error) {
	got := sha256.Sum256([]byte(streamKey))
	if !s.set || subtle.ConstantTimeCompare(got[:], s.sum[:]) != 1 {
		return Grant{}, entity.ErrUnauthorized
	}

	return Grant{ChannelID: StaticChannelID}, nil
}
