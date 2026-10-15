package moderation

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
)

// Audit actions of blocked terms (audit.recorded.v1).
const (
	_auditTermAdded   = "BLOCKED_TERM_ADDED"
	_auditTermRemoved = "BLOCKED_TERM_REMOVED"
	_resourceChannel  = "CHANNEL"
)

// Terms -.
func (uc *UseCase) Terms(ctx context.Context, p entity.Principal, channelID string) ([]entity.BlockedTerm, error) {
	if _, _, err := uc.actor(ctx, p, channelID); err != nil {
		return nil, err
	}

	list, err := uc.r.Terms.Terms(ctx, channelID)
	if err != nil {
		return nil, fmt.Errorf("ModerationUseCase - Terms: %w", err)
	}

	return list, nil
}

// AddTerm blocks a term (at most entity.BlockedTermsMax per channel); a term blocked already in another
// spelling is returned with created false.
func (uc *UseCase) AddTerm(ctx context.Context, p entity.Principal, channelID, raw string) (entity.BlockedTerm, bool, error) {
	display, term, err := entity.TermInput(raw)
	if err != nil {
		return entity.BlockedTerm{}, false, err
	}

	role, _, err := uc.actor(ctx, p, channelID)
	if err != nil {
		return entity.BlockedTerm{}, false, err
	}

	t, created, err := uc.r.Terms.AddTerm(ctx, entity.BlockedTerm{
		ChannelID: channelID, Term: term, Display: display, CreatedBy: entity.UserRef{ID: p.UserID},
	}, entity.BlockedTermsMax, termAudit(p.UserID, role, _auditTermAdded, channelID, display))
	if err != nil {
		if errors.Is(err, entity.ErrLimitReached) {
			return entity.BlockedTerm{}, false, entity.Reject(entity.ErrLimitReached, "limit", fmt.Sprint(entity.BlockedTermsMax))
		}

		return entity.BlockedTerm{}, false, fmt.Errorf("ModerationUseCase - AddTerm: %w", err)
	}

	uc.terms.drop(channelID)

	return t, created, nil
}

// RemoveTerm is idempotent.
func (uc *UseCase) RemoveTerm(ctx context.Context, p entity.Principal, channelID, termID string) error {
	role, _, err := uc.actor(ctx, p, channelID)
	if err != nil {
		return err
	}

	if uuid.Validate(termID) != nil {
		return nil
	}

	if _, err = uc.r.Terms.RemoveTerm(ctx, channelID, termID, termAudit(p.UserID, role, _auditTermRemoved, channelID, "")); err != nil {
		return fmt.Errorf("ModerationUseCase - RemoveTerm: %w", err)
	}

	uc.terms.drop(channelID)

	return nil
}

func termAudit(actorID string, role entity.ActorRole, action, channelID, display string) events.AuditRecorded {
	meta := map[string]any{}
	if display != "" {
		meta["term"] = display
	}

	return events.AuditRecorded{
		ActorID: &actorID, ActorRole: string(role), Action: action, ResourceType: _resourceChannel, ResourceID: &channelID,
		Metadata: meta,
	}
}

// BlockedTerms returns the channel's normalized terms (chat.AutoModSource), cached AUTOMOD_CACHE_TTL.
func (uc *UseCase) BlockedTerms(ctx context.Context, channelID string) ([]string, error) {
	if v, ok := uc.terms.get(channelID, uc.now()); ok {
		terms, _ := v.([]string) //nolint:errcheck // only []string values are stored

		return terms, nil
	}

	list, err := uc.r.Terms.Terms(ctx, channelID)
	if err != nil {
		return nil, fmt.Errorf("ModerationUseCase - BlockedTerms: %w", err)
	}

	terms := make([]string, 0, len(list))
	for _, t := range list {
		terms = append(terms, t.Term)
	}

	uc.terms.set(channelID, terms, uc.now())

	return terms, nil
}

// Pinned returns the room's pinned message (chat.PinSource) with its author's color and badges.
func (uc *UseCase) Pinned(ctx context.Context, streamID string) (*entity.Message, error) {
	m, err := uc.r.Mod.Pinned(ctx, streamID)
	if err != nil || m == nil {
		return m, err //nolint:wrapcheck // the caller logs it
	}

	sc, err := uc.room(ctx, streamID)
	if err != nil {
		return nil, err
	}

	out := uc.decorate(ctx, entity.Channel{ID: sc.Room.ChannelID, OwnerID: sc.OwnerID}, *m)

	return &out, nil
}

// Collect captures report evidence (internal API): a message and the previous ones of its author, or a user's
// last messages; at most entity.EvidenceMax, oldest first.
func (uc *UseCase) Collect(ctx context.Context, streamID string, messageID, userID *string, limit int) ([]entity.EvidenceItem, error) {
	if limit <= 0 || limit > entity.EvidenceMax {
		limit = entity.EvidenceMax
	}

	items, err := uc.r.Mod.Evidence(ctx, streamID, messageID, userID, limit)
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) {
			return nil, err
		}

		return nil, fmt.Errorf("ModerationUseCase - Collect: %w", err)
	}

	return items, nil
}
