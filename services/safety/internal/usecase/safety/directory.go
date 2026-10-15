package safety

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/minhnn/nexuslive-be/services/safety/internal/entity"
	"github.com/minhnn/nexuslive-be/services/safety/internal/repo"
	"github.com/minhnn/nexuslive-be/services/safety/internal/usecase"
)

// Users searches the user copy; locks that expired read as ACTIVE.
func (uc *UseCase) Users(ctx context.Context, q usecase.DirectoryQuery) (usecase.Page[entity.UserCopy], error) {
	term, after, err := directoryQuery(q, func(s string) bool {
		return s == entity.UserActive || s == entity.UserLocked || s == entity.UserDeleted
	})
	if err != nil {
		return usecase.Page[entity.UserCopy]{}, err
	}

	limit := limitOf(q.Limit)

	rows, err := uc.r.Replica.SearchUsers(ctx, term, q.Status, after, limit+1)
	if err != nil {
		return usecase.Page[entity.UserCopy]{}, fmt.Errorf("directory - Users: %w", err)
	}

	now := uc.now()
	for i := range rows {
		rows[i] = rows[i].Effective(now)
	}

	return page(rows, limit, func(u entity.UserCopy) string { return encodeCursor(strings.ToLower(u.Username), u.ID) }), nil
}

// Channels searches the channel copy.
func (uc *UseCase) Channels(ctx context.Context, q usecase.DirectoryQuery) (usecase.Page[entity.ChannelCopy], error) {
	term, after, err := directoryQuery(q, func(s string) bool {
		return s == entity.ChannelActive || s == entity.ChannelSuspended
	})
	if err != nil {
		return usecase.Page[entity.ChannelCopy]{}, err
	}

	limit := limitOf(q.Limit)

	rows, err := uc.r.Replica.SearchChannels(ctx, term, q.Status, after, limit+1)
	if err != nil {
		return usecase.Page[entity.ChannelCopy]{}, fmt.Errorf("directory - Channels: %w", err)
	}

	return page(rows, limit, func(c entity.ChannelCopy) string { return encodeCursor(strings.ToLower(c.Handle), c.ID) }), nil
}

func directoryQuery(q usecase.DirectoryQuery, validStatus func(string) bool) (string, *repo.NameCursor, error) {
	term := strings.TrimSpace(q.Q)
	term = strings.TrimPrefix(term, "@")

	details := []string{}
	if q.Q != "" && (term == "" || utf8.RuneCountInString(term) > _queryMax) {
		details = append(details, "q", entity.DetailLength)
	}

	if q.Status != nil && !validStatus(*q.Status) {
		details = append(details, "status", entity.DetailEnum)
	}

	if len(details) > 0 {
		return "", nil, entity.Reject(entity.ErrValidation, details...)
	}

	after, err := nameCursor(q.Cursor)

	return term, after, err
}

// Channel returns a channel copy, suspended ones included.
func (uc *UseCase) Channel(ctx context.Context, id string) (entity.ChannelCopy, error) {
	if !isUUID(id) {
		return entity.ChannelCopy{}, entity.ErrNotFound
	}

	c, err := uc.r.Replica.Channel(ctx, id)
	if err != nil {
		return c, fmt.Errorf("directory - Channel: %w", err)
	}

	return c, nil
}

// AuditLog implements usecase.Audit (its List differs from the report queue's).
type AuditLog struct{ uc *UseCase }

// AuditLog -.
func (uc *UseCase) AuditLog() AuditLog { return AuditLog{uc: uc} }

// List returns the audit log, newest first, with the actors' usernames.
func (a AuditLog) List(ctx context.Context, q usecase.AuditQuery) (usecase.Page[entity.AuditEntry], error) {
	if details := auditDetails(q); len(details) > 0 {
		return usecase.Page[entity.AuditEntry]{}, entity.Reject(entity.ErrValidation, details...)
	}

	after, err := timeCursor(q.Cursor)
	if err != nil {
		return usecase.Page[entity.AuditEntry]{}, err
	}

	limit := limitOf(q.Limit)

	f, found, err := a.actorFilter(ctx, q)
	if err != nil || !found {
		return usecase.Page[entity.AuditEntry]{Items: []entity.AuditEntry{}}, err
	}

	rows, err := a.uc.r.Audit.List(ctx, f, after, limit+1)
	if err != nil {
		return usecase.Page[entity.AuditEntry]{}, fmt.Errorf("audit - List: %w", err)
	}

	p := page(rows, limit, func(e entity.AuditEntry) string { return encodeCursor(e.CreatedAt.Format(time.RFC3339Nano), e.ID) })

	ids := repo.LookupIDs{}
	for _, e := range p.Items {
		ids.Users = appendRef(ids.Users, e.Actor)
	}

	l, err := a.uc.r.Replica.Lookup(ctx, ids)
	if err != nil {
		return usecase.Page[entity.AuditEntry]{}, fmt.Errorf("audit - List - actors: %w", err)
	}

	for i := range p.Items {
		p.Items[i].Actor = refPtr(l, p.Items[i].Actor)
	}

	return p, nil
}

// actorFilter turns the actor username into ActorID; found is false when no user copy has that exact name.
func (a AuditLog) actorFilter(ctx context.Context, q usecase.AuditQuery) (repo.AuditFilter, bool, error) {
	name := strings.TrimPrefix(strings.TrimSpace(q.Actor), "@")
	if q.ActorID != nil || name == "" {
		return q.AuditFilter, true, nil
	}

	// Prefix search ordered by lower(username): an exact match comes first.
	users, err := a.uc.r.Replica.SearchUsers(ctx, name, nil, nil, 1)
	if err != nil {
		return repo.AuditFilter{}, false, fmt.Errorf("audit - List - actor: %w", err)
	}

	if len(users) == 0 || !strings.EqualFold(users[0].Username, name) {
		return repo.AuditFilter{}, false, nil
	}

	f := q.AuditFilter
	f.ActorID = &users[0].ID

	return f, true, nil
}

func auditDetails(q usecase.AuditQuery) []string {
	details := []string{}
	if q.ActorID != nil && !isUUID(*q.ActorID) {
		details = append(details, "actorId", entity.DetailInvalid)
	}

	if q.ResourceID != nil && !isUUID(*q.ResourceID) {
		details = append(details, "resourceId", entity.DetailInvalid)
	}

	if q.From != nil && q.To != nil && !q.From.Before(*q.To) {
		details = append(details, "to", entity.DetailInvalid)
	}

	return details
}

// Record stores the audit row of a domain event once.
func (a AuditLog) Record(ctx context.Context, e entity.AuditEntry, promote *string) error {
	if err := a.uc.r.Audit.Insert(ctx, e, promote); err != nil {
		return fmt.Errorf("audit - Record: %w", err)
	}

	return nil
}
