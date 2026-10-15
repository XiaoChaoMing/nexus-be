// Package repo implements application outer layer logic. Each logic group in own file.
package repo

import (
	"context"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
)

type (
	// NewEmailToken is a freshly generated one-time e-mail token. Raw goes into the
	// auth.email_requested.v1 outbox event only; the table stores Hash.
	NewEmailToken struct {
		Raw       string
		Hash      string
		Purpose   entity.TokenPurpose
		CreatedAt time.Time
		ExpiresAt time.Time
	}

	// LoginFailurePolicy counts failures within Window and locks for Window once MaxFailures is reached.
	LoginFailurePolicy struct {
		MaxFailures int
		Window      time.Duration
	}

	// ProfilePatch holds the PATCH /v1/me fields; nil means unchanged.
	ProfilePatch struct {
		DisplayName *string
		BirthDate   *time.Time
		Locale      *string
		Timezone    *string
		// Preferences keys to overwrite (merged into the stored jsonb).
		Preferences map[string]bool
	}

	// ApplicationCursor is the keyset position of the admin application list (oldest first).
	ApplicationCursor struct {
		CreatedAt time.Time
		ID        string
	}

	// CreatorState is what decides whether a user may submit a creator application.
	CreatorState struct {
		HasChannel  bool
		HasApproved bool
		HasPending  bool
	}

	// UserRepo -.
	UserRepo interface {
		// Create stores the user, its password hash, a VERIFY e-mail token and the user.registered and
		// auth.email_requested outbox events in one transaction.
		Create(ctx context.Context, user entity.User, passwordHash string, verify NewEmailToken) error
		// GetByLogin looks a user up by email or username (case-insensitive) and returns its password hash.
		GetByLogin(ctx context.Context, login string) (entity.User, string, error)
		GetByID(ctx context.Context, id string) (entity.User, error)
		GetByEmail(ctx context.Context, email string) (entity.User, error)
		PasswordHash(ctx context.Context, userID string) (string, error)
		// RecordLoginFailure counts one wrong password atomically and returns the updated user; it locks the
		// account (LOCKED + locked_until) when the policy's limit is reached. Permanently locked or deleted
		// accounts are left untouched (ErrUserNotFound).
		RecordLoginFailure(ctx context.Context, userID string, now time.Time, p LoginFailurePolicy) (entity.User, error)
		// ResetLoginFailures clears the counter and reactivates an expired temporary lock.
		ResetLoginFailures(ctx context.Context, userID string, now time.Time) error
		// UpdateProfile applies the patch (ErrBirthDateAlreadySet when a birth date is already stored) and
		// writes user.profile_updated when the display name changes.
		UpdateProfile(ctx context.Context, userID string, p ProfilePatch, now time.Time) (entity.User, error)
		// ChangePassword stores the new hash, ends every session and stores next, in one transaction.
		ChangePassword(ctx context.Context, userID, passwordHash string, next entity.Session) error
		// PromoteAdmin sets global_role = ADMIN for the account with this e-mail.
		PromoteAdmin(ctx context.Context, email string) (entity.User, error)
	}

	// SessionRepo -.
	SessionRepo interface {
		Create(ctx context.Context, s entity.Session) error
		GetByTokenHash(ctx context.Context, hash string) (entity.Session, error)
		// Rotate revokes oldID and stores next atomically; ErrSessionAlreadyRevoked if oldID was already revoked.
		Rotate(ctx context.Context, oldID string, next entity.Session) error
		Revoke(ctx context.Context, id string) error
		RevokeAllForUser(ctx context.Context, userID string) error
	}

	// TokenRepo stores one-time e-mail tokens (email_tokens).
	TokenRepo interface {
		// Stats summarizes the user's tokens of this purpose created since windowStart.
		Stats(ctx context.Context, userID string, purpose entity.TokenPurpose, windowStart time.Time) (entity.TokenStats, error)
		// Issue supersedes the unused tokens of the same purpose, stores t and writes
		// auth.email_requested in one transaction.
		Issue(ctx context.Context, user entity.User, t NewEmailToken) error
		// IssueLimited checks limit and issues t in one transaction serialized per user, so concurrent
		// requests cannot both pass the check. A positive wait means nothing was issued.
		IssueLimited(ctx context.Context, user entity.User, t NewEmailToken, limit entity.TokenLimit) (wait time.Duration, err error)
		// VerifyEmail consumes a VERIFY token, marks the e-mail verified and writes user.email_verified;
		// ErrTokenInvalid when the token is unknown, expired or used. Returns the user id.
		VerifyEmail(ctx context.Context, tokenHash string, now time.Time) (string, error)
		// ResetPassword consumes a RESET token, stores the hash, ends every session, clears the failure
		// counter and lifts a temporary lock. Returns the user id.
		ResetPassword(ctx context.Context, tokenHash, passwordHash string, now time.Time) (string, error)
	}

	// ApplicationRepo stores creator applications.
	ApplicationRepo interface {
		CreatorState(ctx context.Context, userID string) (CreatorState, error)
		// Create stores a PENDING application and creator.application_submitted; ErrApplicationPending when
		// another PENDING one exists.
		Create(ctx context.Context, a entity.Application) error
		// Latest returns the user's newest application, nil when there is none.
		Latest(ctx context.Context, userID string) (*entity.Application, error)
		// Cancel withdraws the user's PENDING application; ErrApplicationNotFound / ErrInvalidState.
		Cancel(ctx context.Context, userID, id string, now time.Time) error
		// List returns applications with their applicant, oldest first; an empty status means all.
		List(ctx context.Context, status entity.ApplicationStatus, after *ApplicationCursor, limit int) ([]entity.Application, error)
		// Get returns one application with its applicant.
		Get(ctx context.Context, id string) (entity.Application, error)
		// Approve moves a PENDING application to APPROVED, copies its birth date to the user and writes
		// creator.approved; ErrInvalidState when it is no longer PENDING.
		Approve(ctx context.Context, adminID, id string, now time.Time) error
		// Reject moves a PENDING application to REJECTED and writes creator.rejected.
		Reject(ctx context.Context, adminID, id, reason string, now time.Time) error
	}

	// ChannelRepo keeps user_channels in sync with channel-svc.
	ChannelRepo interface {
		// UpsertChannel stores the channel once per event id; ErrUserNotFound when the owner is unknown,
		// ErrChannelTaken when the channel id belongs to another user.
		UpsertChannel(ctx context.Context, eventID string, ch entity.UserChannel) error
	}

	// BlockCursor is the keyset position of the block list (created_at DESC, blocked id DESC).
	BlockCursor struct {
		CreatedAt time.Time
		UserID    string
	}

	// BlockRepo stores user_blocks; changes queue user.blocked / user.unblocked in the same transaction.
	BlockRepo interface {
		// List returns the user's blocks, newest first, with the blocked accounts' names.
		List(ctx context.Context, userID string, after *BlockCursor, limit int) ([]entity.Block, error)
		// IDs returns at most limit blocked user ids.
		IDs(ctx context.Context, userID string, limit int) ([]string, error)
		// Add blocks targetID; added is false when it was already blocked. entity.ErrNotFound when the target
		// does not exist or was deleted, entity.ErrBlockLimit when the user already blocks maxBlocks accounts.
		Add(ctx context.Context, userID, targetID string, maxBlocks int) (added bool, err error)
		// Remove reports false when there was nothing to remove.
		Remove(ctx context.Context, userID, targetID string) (bool, error)
	}

	// ModerationRepo applies the account actions of safety-svc.
	ModerationRepo interface {
		// ApplyUserAction locks or unlocks the account once per event id, ends its sessions on a lock and queues
		// user.locked / user.unlocked; changed is false when nothing changed (admin target, deleted, not locked).
		ApplyUserAction(ctx context.Context, eventID string, e events.UserActioned) (changed bool, err error)
	}

	// SnapshotRepo pages through users and blocks for other services' one-off backfill (internal API).
	SnapshotRepo interface {
		// Users returns up to limit users with id > after (any status), ordered by id.
		Users(ctx context.Context, after string, limit int) ([]entity.User, error)
		// Blocks returns up to limit rows after the (blocker, blocked) position, ordered by it.
		Blocks(ctx context.Context, after *entity.BlockPair, limit int) ([]entity.BlockPair, error)
	}

	// SeedRepo creates the development accounts.
	SeedRepo interface {
		// EnsureUser inserts the user (e-mail verified) with its password hash unless the username or the
		// e-mail exists, and returns the stored user.
		EnsureUser(ctx context.Context, u entity.User, passwordHash string) (entity.User, bool, error)
		// EnsureApproved stores an APPROVED application reviewed by adminID and creator.approved, unless
		// the user already has an approved application. Reports whether it created one.
		EnsureApproved(ctx context.Context, a entity.Application, adminID string) (bool, error)
	}
)
