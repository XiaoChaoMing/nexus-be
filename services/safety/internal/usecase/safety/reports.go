package safety

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/minhnn/nexuslive-be/services/safety/internal/entity"
	"github.com/minhnn/nexuslive-be/services/safety/internal/usecase"
)

// File validates the report, returns an open duplicate as is (BR-F15), applies REPORT_RATE_LIMIT, checks the
// target on the copies (or chat-svc for a message) and stores it with the evidence.
func (uc *UseCase) File(ctx context.Context, reporterID string, in usecase.ReportInput) (entity.Report, bool, error) {
	in, err := validateReport(in)
	if err != nil {
		return entity.Report{}, false, err
	}

	dup, err := uc.r.Reports.OpenDuplicate(ctx, reporterID, in.TargetType, in.TargetID)
	if err != nil {
		return entity.Report{}, false, fmt.Errorf("reports - File: %w", err)
	}

	if dup != nil {
		return *dup, false, nil
	}

	if err := uc.checkRate(ctx, reporterID); err != nil {
		return entity.Report{}, false, err
	}

	r := entity.Report{
		ReporterID: reporterID, TargetType: in.TargetType, TargetID: in.TargetID, Reason: in.Reason, Note: in.Note,
		StreamID: in.StreamID, Evidence: []entity.EvidenceItem{}, EvidenceStatus: entity.EvidenceNone,
	}

	if err := uc.checkTarget(ctx, &r); err != nil {
		return entity.Report{}, false, err
	}

	out, created, err := uc.r.Reports.Insert(ctx, r)
	if err != nil {
		return entity.Report{}, false, fmt.Errorf("reports - File: %w", err)
	}

	if created {
		if err := uc.r.Rate.Hit(ctx, reporterID, _rateWindow); err != nil {
			uc.l.Warn("safety - report rate hit: %v", err)
		}

		uc.obs.ReportFiled(out.TargetType, out.Reason)
	}

	return out, created, nil
}

func validateReport(in usecase.ReportInput) (usecase.ReportInput, error) {
	details := []string{}

	switch {
	case in.TargetType == "":
		details = append(details, "targetType", entity.DetailRequired)
	case !entity.ValidTarget(in.TargetType):
		details = append(details, "targetType", entity.DetailEnum)
	}

	switch {
	case in.TargetID == "":
		details = append(details, "targetId", entity.DetailRequired)
	case !isUUID(in.TargetID):
		details = append(details, "targetId", entity.DetailInvalid)
	}

	switch {
	case in.Reason == "":
		details = append(details, "reason", entity.DetailRequired)
	case !entity.ValidReason(in.Reason):
		details = append(details, "reason", entity.DetailEnum)
	}

	details = append(details, validateNote(&in)...)
	details = append(details, validateStream(&in)...)

	if len(details) > 0 {
		return in, entity.Reject(entity.ErrValidation, details...)
	}

	return in, nil
}

func validateNote(in *usecase.ReportInput) []string {
	if in.Note == nil {
		return nil
	}

	note, ok := trimmed(*in.Note, 0, entity.ReportNoteMax)
	if !ok {
		return []string{"note", entity.DetailLength}
	}

	in.Note = &note
	if note == "" {
		in.Note = nil
	}

	return nil
}

func validateStream(in *usecase.ReportInput) []string {
	if in.StreamID != nil && strings.TrimSpace(*in.StreamID) == "" {
		in.StreamID = nil
	}

	switch {
	case in.StreamID == nil && in.TargetType == entity.TargetMessage:
		return []string{"streamId", entity.DetailRequired}
	case in.StreamID != nil && !isUUID(*in.StreamID):
		return []string{"streamId", entity.DetailInvalid}
	}

	return nil
}

func (uc *UseCase) checkRate(ctx context.Context, reporterID string) error {
	n, ttl, err := uc.r.Rate.Count(ctx, reporterID)
	if err != nil {
		return fmt.Errorf("reports - rate: %w", err)
	}

	if n < uc.rateLimit {
		return nil
	}

	if ttl <= 0 {
		ttl = _rateWindow
	}

	return &entity.RateLimitError{RetryAfter: ttl}
}

// checkTarget resolves the user behind the target and refuses reporting oneself (§6.5).
func (uc *UseCase) checkTarget(ctx context.Context, r *entity.Report) error {
	switch r.TargetType {
	case entity.TargetUser:
		return uc.checkUser(ctx, r)
	case entity.TargetChannel:
		c, err := uc.r.Replica.Channel(ctx, r.TargetID)
		if err != nil {
			return fmt.Errorf("reports - channel: %w", err)
		}

		return own(r, c.OwnerID)
	case entity.TargetStream:
		return uc.checkStream(ctx, r)
	}

	return uc.checkMessage(ctx, r)
}

func own(r *entity.Report, userID string) error {
	if userID == r.ReporterID {
		return entity.Invalid("targetId")
	}

	r.TargetUserID = &userID

	return nil
}

func (uc *UseCase) checkUser(ctx context.Context, r *entity.Report) error {
	u, err := uc.r.Replica.User(ctx, r.TargetID)
	if err != nil {
		return fmt.Errorf("reports - user: %w", err)
	}

	if u.Status == entity.UserDeleted {
		return entity.ErrNotFound
	}

	if err := own(r, u.ID); err != nil {
		return err
	}

	if r.StreamID == nil {
		return nil
	}

	// A missing room or no messages leaves the evidence empty.
	return uc.capture(ctx, r, "", r.TargetID)
}

func (uc *UseCase) checkStream(ctx context.Context, r *entity.Report) error {
	s, err := uc.r.Replica.Stream(ctx, r.TargetID)
	if err != nil {
		return fmt.Errorf("reports - stream: %w", err)
	}

	r.StreamID = &s.ID

	c, err := uc.r.Replica.Channel(ctx, s.ChannelID)
	if errors.Is(err, entity.ErrNotFound) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("reports - stream channel: %w", err)
	}

	return own(r, c.OwnerID)
}

// checkMessage captures the evidence; chat-svc being down or slow still files the report (AF-F10).
func (uc *UseCase) checkMessage(ctx context.Context, r *entity.Report) error {
	if err := uc.capture(ctx, r, r.TargetID, ""); err != nil {
		return err
	}

	for _, it := range r.Evidence {
		if it.MessageID == r.TargetID {
			return own(r, it.UserID)
		}
	}

	return nil
}

// capture asks chat-svc for the evidence: ErrNotFound only for a message; other errors mark it UNAVAILABLE.
func (uc *UseCase) capture(ctx context.Context, r *entity.Report, messageID, userID string) error {
	cctx, cancel := context.WithTimeout(ctx, uc.evidenceTimeout)
	defer cancel()

	start := time.Now()
	items, err := uc.r.Evidence.Evidence(cctx, *r.StreamID, messageID, userID, entity.EvidenceCount)

	switch {
	case err == nil && len(items) > 0:
		r.Evidence, r.EvidenceStatus = items, entity.EvidenceCaptured
	case err == nil:
	case errors.Is(err, entity.ErrNotFound) && messageID != "":
		return entity.ErrNotFound
	case errors.Is(err, entity.ErrNotFound):
	default:
		uc.l.Warn("safety - evidence unavailable after %s: %v", time.Since(start), err)

		r.EvidenceStatus = entity.EvidenceUnavailable
	}

	return nil
}
