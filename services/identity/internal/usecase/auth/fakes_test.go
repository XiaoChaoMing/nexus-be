package auth_test

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/repo"
)

// memStore is an in-memory database that enforces the same uniqueness, revoke, token and lockout
// rules as the SQL schema and queries.
type memStore struct {
	mu       sync.Mutex
	issueMu  sync.Mutex
	users    map[string]entity.User
	hashes   map[string]string
	sessions map[string]entity.Session
	tokens   []memToken
	// outbox holds the event types written, in order.
	outbox []string
}

type memToken struct {
	userID    string
	hash      string
	raw       string
	purpose   entity.TokenPurpose
	createdAt time.Time
	expiresAt time.Time
	used      bool
}

func newMemStore() *memStore {
	return &memStore{users: map[string]entity.User{}, hashes: map[string]string{}, sessions: map[string]entity.Session{}}
}

func (s *memStore) user(id string) entity.User {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.users[id]
}

func (s *memStore) setUser(u entity.User) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.users[u.ID] = u
}

// lastToken returns the raw value of the newest token of the user and purpose.
func (s *memStore) lastToken(userID string, purpose entity.TokenPurpose) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := len(s.tokens) - 1; i >= 0; i-- {
		if t := s.tokens[i]; t.userID == userID && t.purpose == purpose {
			return t.raw
		}
	}

	return ""
}

func (s *memStore) tokenCount(userID string, purpose entity.TokenPurpose) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := 0

	for _, t := range s.tokens {
		if t.userID == userID && t.purpose == purpose {
			n++
		}
	}

	return n
}

func (s *memStore) events() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.outbox...)
}

// issueLocked mirrors issueToken: supersede, insert, outbox. Callers hold mu.
func (s *memStore) issueLocked(userID string, t repo.NewEmailToken) {
	for i := range s.tokens {
		if s.tokens[i].userID == userID && s.tokens[i].purpose == t.Purpose {
			s.tokens[i].used = true
		}
	}

	s.tokens = append(s.tokens, memToken{
		userID: userID, hash: t.Hash, raw: t.Raw, purpose: t.Purpose, createdAt: t.CreatedAt, expiresAt: t.ExpiresAt,
	})
	s.outbox = append(s.outbox, "auth.email_requested.v1:"+string(t.Purpose))
}

// endSessionsLocked mirrors setPassword: the sessions are deleted, not revoked.
func (s *memStore) endSessionsLocked(userID string) {
	for id, x := range s.sessions {
		if x.UserID == userID {
			delete(s.sessions, id)
		}
	}
}

func (s *memStore) consumeLocked(hash string, purpose entity.TokenPurpose, now time.Time) (string, error) {
	for i, t := range s.tokens {
		if t.hash == hash && t.purpose == purpose && !t.used && now.Before(t.expiresAt) {
			s.tokens[i].used = true

			return t.userID, nil
		}
	}

	return "", entity.ErrTokenInvalid
}

func (s *memStore) revokeAllLocked(userID string, now time.Time) {
	for id, x := range s.sessions {
		if x.UserID == userID && x.RevokedAt == nil {
			t := now
			x.RevokedAt = &t
			s.sessions[id] = x
		}
	}
}

type memUsers struct{ s *memStore }

func (m memUsers) Create(_ context.Context, u entity.User, hash string, verify repo.NewEmailToken) error {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()

	for _, x := range m.s.users {
		if strings.EqualFold(x.Username, u.Username) {
			return entity.ErrUsernameTaken
		}

		if strings.EqualFold(x.Email, u.Email) {
			return entity.ErrEmailTaken
		}
	}

	m.s.users[u.ID] = u
	m.s.hashes[u.ID] = hash
	m.s.outbox = append(m.s.outbox, "user.registered.v1")
	m.s.issueLocked(u.ID, verify)

	return nil
}

func (m memUsers) GetByLogin(_ context.Context, login string) (entity.User, string, error) {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()

	for _, x := range m.s.users {
		if strings.EqualFold(x.Username, login) || strings.EqualFold(x.Email, login) {
			return x, m.s.hashes[x.ID], nil
		}
	}

	return entity.User{}, "", entity.ErrUserNotFound
}

func (m memUsers) GetByID(_ context.Context, id string) (entity.User, error) {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()

	u, ok := m.s.users[id]
	if !ok {
		return entity.User{}, entity.ErrUserNotFound
	}

	return u, nil
}

func (m memUsers) GetByEmail(_ context.Context, email string) (entity.User, error) {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()

	for _, x := range m.s.users {
		if strings.EqualFold(x.Email, email) {
			return x, nil
		}
	}

	return entity.User{}, entity.ErrUserNotFound
}

func (m memUsers) PasswordHash(_ context.Context, userID string) (string, error) {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()

	h, ok := m.s.hashes[userID]
	if !ok {
		return "", entity.ErrUserNotFound
	}

	return h, nil
}

func (m memUsers) RecordLoginFailure(_ context.Context, userID string, now time.Time, p repo.LoginFailurePolicy) (entity.User, error) {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()

	u, ok := m.s.users[userID]
	expiredLock := u.Status == entity.UserLocked && u.LockedUntil != nil && !now.Before(*u.LockedUntil)

	if !ok || (u.Status != entity.UserActive && !expiredLock) {
		return entity.User{}, entity.ErrUserNotFound
	}

	n := 1
	if u.LastFailedLoginAt != nil && u.LastFailedLoginAt.After(now.Add(-p.Window)) {
		n = u.FailedLogins + 1
	}

	at := now
	u.FailedLogins, u.LastFailedLoginAt, u.Status, u.LockedUntil = n, &at, entity.UserActive, nil

	if n >= p.MaxFailures {
		until := now.Add(p.Window)
		u.Status, u.LockedUntil = entity.UserLocked, &until
	}

	m.s.users[userID] = u

	return u, nil
}

func (m memUsers) ResetLoginFailures(_ context.Context, userID string, now time.Time) error {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()

	u := m.s.users[userID]
	u.FailedLogins, u.LastFailedLoginAt = 0, nil

	if u.Status == entity.UserLocked && u.LockedUntil != nil && !now.Before(*u.LockedUntil) {
		u.Status, u.LockedUntil = entity.UserActive, nil
	}

	m.s.users[userID] = u

	return nil
}

func (m memUsers) UpdateProfile(_ context.Context, userID string, p repo.ProfilePatch, _ time.Time) (entity.User, error) {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()

	u, ok := m.s.users[userID]
	if !ok {
		return entity.User{}, entity.ErrUserNotFound
	}

	if p.BirthDate != nil && u.BirthDate != nil {
		return entity.User{}, entity.ErrBirthDateAlreadySet
	}

	if p.DisplayName != nil && *p.DisplayName != u.DisplayName {
		u.DisplayName = *p.DisplayName

		m.s.outbox = append(m.s.outbox, "user.profile_updated.v1")
	}

	if p.BirthDate != nil {
		u.BirthDate = p.BirthDate
	}

	m.s.users[userID] = applySettings(u, p)

	return m.s.users[userID], nil
}

func applySettings(u entity.User, p repo.ProfilePatch) entity.User {
	if v, ok := p.Preferences["sidebarCollapsed"]; ok {
		u.Preferences.SidebarCollapsed = v
	}

	if v, ok := p.Preferences["matureVisible"]; ok {
		u.Preferences.MatureVisible = v
	}

	if p.Locale != nil {
		u.Locale = *p.Locale
	}

	if p.Timezone != nil {
		u.Timezone = *p.Timezone
	}

	return u
}

func (m memUsers) ChangePassword(_ context.Context, userID, hash string, next entity.Session) error {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()

	m.s.hashes[userID] = hash
	m.s.endSessionsLocked(userID)
	m.s.sessions[next.ID] = next

	return nil
}

func (m memUsers) PromoteAdmin(context.Context, string) (entity.User, error) {
	return entity.User{}, entity.ErrUserNotFound
}

type memSessions struct{ s *memStore }

func (m memSessions) Create(_ context.Context, se entity.Session) error {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()

	m.s.sessions[se.ID] = se

	return nil
}

func (m memSessions) GetByTokenHash(_ context.Context, hash string) (entity.Session, error) {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()

	for _, x := range m.s.sessions {
		if x.RefreshTokenHash == hash {
			return x, nil
		}
	}

	return entity.Session{}, entity.ErrSessionNotFound
}

func (m memSessions) Rotate(_ context.Context, oldID string, next entity.Session) error {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()

	old := m.s.sessions[oldID]
	if old.RevokedAt != nil {
		return entity.ErrSessionAlreadyRevoked
	}

	t := next.CreatedAt
	old.RevokedAt = &t
	m.s.sessions[oldID] = old
	m.s.sessions[next.ID] = next

	return nil
}

func (m memSessions) Revoke(_ context.Context, id string) error {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()

	x := m.s.sessions[id]
	if x.RevokedAt == nil {
		t := time.Now()
		x.RevokedAt = &t
		m.s.sessions[id] = x
	}

	return nil
}

func (m memSessions) RevokeAllForUser(_ context.Context, userID string) error {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()

	m.s.revokeAllLocked(userID, time.Now())

	return nil
}

func (m memSessions) activeFor(userID string) int {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()

	n := 0

	for _, x := range m.s.sessions {
		if x.UserID == userID && x.RevokedAt == nil {
			n++
		}
	}

	return n
}

type memTokens struct{ s *memStore }

func (m memTokens) Stats(_ context.Context, userID string, purpose entity.TokenPurpose, windowStart time.Time) (entity.TokenStats, error) {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()

	var st entity.TokenStats

	for _, t := range m.s.tokens {
		if t.userID != userID || t.purpose != purpose {
			continue
		}

		at := t.createdAt
		if st.LastIssuedAt == nil || at.After(*st.LastIssuedAt) {
			st.LastIssuedAt = &at
		}

		if at.After(windowStart) {
			st.IssuedInWindow++

			if st.OldestInWindow == nil || at.Before(*st.OldestInWindow) {
				st.OldestInWindow = &at
			}
		}
	}

	return st, nil
}

func (m memTokens) Issue(_ context.Context, user entity.User, t repo.NewEmailToken) error {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()

	m.s.issueLocked(user.ID, t)

	return nil
}

func (m memTokens) IssueLimited(ctx context.Context, user entity.User, t repo.NewEmailToken, limit entity.TokenLimit) (time.Duration, error) {
	// One lock across check and issue, like the row lock in the SQL implementation.
	m.s.issueMu.Lock()
	defer m.s.issueMu.Unlock()

	stats, err := m.Stats(ctx, user.ID, t.Purpose, t.CreatedAt.Add(-limit.Window))
	if err != nil {
		return 0, err
	}

	if wait := limit.RetryAfter(stats, t.CreatedAt); wait > 0 {
		return wait, nil
	}

	return 0, m.Issue(ctx, user, t)
}

func (m memTokens) VerifyEmail(_ context.Context, hash string, now time.Time) (string, error) {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()

	userID, err := m.s.consumeLocked(hash, entity.TokenVerify, now)
	if err != nil {
		return "", err
	}

	u := m.s.users[userID]
	if u.EmailVerifiedAt == nil {
		at := now
		u.EmailVerifiedAt = &at
		m.s.users[userID] = u
		m.s.outbox = append(m.s.outbox, "user.email_verified.v1")
	}

	return userID, nil
}

func (m memTokens) ResetPassword(_ context.Context, hash, passwordHash string, now time.Time) (string, error) {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()

	userID, err := m.s.consumeLocked(hash, entity.TokenReset, now)
	if err != nil {
		return "", err
	}

	m.s.hashes[userID] = passwordHash
	m.s.endSessionsLocked(userID)

	u := m.s.users[userID]
	u.FailedLogins, u.LastFailedLoginAt = 0, nil

	if u.Status == entity.UserLocked && u.LockedUntil != nil {
		u.Status, u.LockedUntil = entity.UserActive, nil
	}

	m.s.users[userID] = u

	return userID, nil
}
