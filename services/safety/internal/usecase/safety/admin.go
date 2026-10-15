package safety

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/minhnn/nexuslive-be/services/safety/internal/entity"
	"github.com/minhnn/nexuslive-be/services/safety/internal/repo"
	"github.com/minhnn/nexuslive-be/services/safety/internal/usecase"
)

// List is the admin queue, oldest first; status defaults to OPEN, IN_REVIEW.
func (uc *UseCase) List(ctx context.Context, q usecase.ReportQuery) (usecase.Page[entity.ReportView], error) {
	f, err := reportFilter(q)
	if err != nil {
		return usecase.Page[entity.ReportView]{}, err
	}

	after, err := timeCursor(q.Cursor)
	if err != nil {
		return usecase.Page[entity.ReportView]{}, err
	}

	limit := limitOf(q.Limit)

	rows, err := uc.r.Reports.List(ctx, f, after, limit+1)
	if err != nil {
		return usecase.Page[entity.ReportView]{}, fmt.Errorf("adminreports - List: %w", err)
	}

	p := page(rows, limit, func(r entity.Report) string { return encodeCursor(r.CreatedAt.Format(time.RFC3339Nano), r.ID) })

	views, err := uc.views(ctx, p.Items)
	if err != nil {
		return usecase.Page[entity.ReportView]{}, err
	}

	return usecase.Page[entity.ReportView]{Items: views, Next: p.Next}, nil
}

func reportFilter(q usecase.ReportQuery) (repo.ReportFilter, error) {
	f := repo.ReportFilter{Statuses: q.Statuses, TargetTypes: q.TargetTypes, Reasons: q.Reasons}
	if len(f.Statuses) == 0 {
		f.Statuses = []string{entity.ReportOpen, entity.ReportInReview}
	}

	details := []string{}
	details = append(details, enumDetail("status", f.Statuses, entity.ValidStatus)...)
	details = append(details, enumDetail("targetType", f.TargetTypes, entity.ValidTarget)...)
	details = append(details, enumDetail("reason", f.Reasons, entity.ValidReason)...)

	if len(details) > 0 {
		return f, entity.Reject(entity.ErrValidation, details...)
	}

	return f, nil
}

func enumDetail(field string, values []string, valid func(string) bool) []string {
	for _, v := range values {
		if !valid(v) {
			return []string{field, entity.DetailEnum}
		}
	}

	return nil
}

// OpenCount -.
func (uc *UseCase) OpenCount(ctx context.Context) (open, inReview int, err error) {
	open, inReview, err = uc.r.Reports.Counts(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("adminreports - OpenCount: %w", err)
	}

	return open, inReview, nil
}

// Get returns the report with related reports and the admin actions on its target.
func (uc *UseCase) Get(ctx context.Context, id string) (entity.ReportDetail, error) {
	if !isUUID(id) {
		return entity.ReportDetail{}, entity.ErrNotFound
	}

	r, err := uc.r.Reports.Get(ctx, id)
	if err != nil {
		return entity.ReportDetail{}, fmt.Errorf("adminreports - Get: %w", err)
	}

	related, err := uc.r.Reports.Related(ctx, r, _relatedMax)
	if err != nil {
		return entity.ReportDetail{}, fmt.Errorf("adminreports - Get - related: %w", err)
	}

	actions, err := uc.history(ctx, r)
	if err != nil {
		return entity.ReportDetail{}, err
	}

	views, err := uc.views(ctx, append([]entity.Report{r}, related...))
	if err != nil {
		return entity.ReportDetail{}, err
	}

	l, err := uc.r.Replica.Lookup(ctx, detailIDs(r, actions))
	if err != nil {
		return entity.ReportDetail{}, fmt.Errorf("adminreports - Get - lookup: %w", err)
	}

	for i := range actions {
		actions[i].Admin = ref(l, actions[i].AdminID)
	}

	authors := map[string]entity.UserCopy{}

	for _, e := range r.Evidence {
		if u, ok := l.Users[e.UserID]; ok {
			authors[e.UserID] = u
		}
	}

	return entity.ReportDetail{
		ReportView: views[0], Related: views[1:], Actions: actions, ChannelID: reportChannel(l, r), Authors: authors,
	}, nil
}

// history merges the admin actions on the target with the chat timeouts and bans of the user behind it (from
// the audit log), newest first, at most _relatedMax.
func (uc *UseCase) history(ctx context.Context, r entity.Report) ([]entity.AdminAction, error) {
	actions, err := uc.r.Actions.ForReport(ctx, r, _relatedMax)
	if err != nil {
		return nil, fmt.Errorf("adminreports - Get - actions: %w", err)
	}

	for i := range actions {
		actions[i].ActorRole = entity.RoleAdmin
	}

	if r.TargetUserID == nil {
		return actions, nil
	}

	user := entity.TargetUser

	chat, err := uc.r.Audit.List(ctx, repo.AuditFilter{
		ResourceType: &user, ResourceID: r.TargetUserID, Actions: entity.ChatRestrictionActions(),
	}, nil, _relatedMax)
	if err != nil {
		return nil, fmt.Errorf("adminreports - Get - chat actions: %w", err)
	}

	for _, e := range chat {
		actions = append(actions, chatAction(e))
	}

	slices.SortStableFunc(actions, func(a, b entity.AdminAction) int { return b.CreatedAt.Compare(a.CreatedAt) })

	return actions[:min(len(actions), _relatedMax)], nil
}

// chatAction reads a chat restriction from its audit row (chat.mod_action.v1 metadata).
func chatAction(e entity.AuditEntry) entity.AdminAction {
	a := entity.AdminAction{ID: e.ID, ActorRole: e.ActorRole, Type: e.Action, TargetType: e.ResourceType, CreatedAt: e.CreatedAt}

	if e.ActorID != nil {
		a.AdminID = *e.ActorID
	}

	if e.ResourceID != nil {
		a.TargetID = *e.ResourceID
	}

	if e.Reason != nil {
		a.Reason = *e.Reason
	}

	if c, ok := e.Metadata["channelId"].(string); ok {
		a.ChannelID = &c
	}

	if s, ok := e.Metadata["expiresAt"].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			a.ExpiresAt = &t
		}
	}

	return a
}

// detailIDs are the actors of the history, the evidence authors and the stream of the report.
func detailIDs(r entity.Report, actions []entity.AdminAction) repo.LookupIDs {
	ids := repo.LookupIDs{}

	for _, a := range actions {
		if a.AdminID != "" {
			ids.Users = append(ids.Users, a.AdminID)
		}
	}

	for _, e := range r.Evidence {
		ids.Users = append(ids.Users, e.UserID)
	}

	if r.TargetType == entity.TargetStream {
		ids.Streams = append(ids.Streams, r.TargetID)
	} else if r.StreamID != nil {
		ids.Streams = append(ids.Streams, *r.StreamID)
	}

	return ids
}

// reportChannel is the reported channel, or the channel of the reported stream / of the room of the report.
func reportChannel(l repo.Lookup, r entity.Report) *string {
	streamID := r.StreamID

	switch r.TargetType {
	case entity.TargetChannel:
		return &r.TargetID
	case entity.TargetStream:
		streamID = &r.TargetID
	}

	if streamID == nil {
		return nil
	}

	s, ok := l.Streams[*streamID]
	if !ok || s.ChannelID == "" {
		return nil
	}

	return &s.ChannelID
}

// Claim -.
func (uc *UseCase) Claim(ctx context.Context, adminID, id string) (entity.ReportView, error) {
	if !isUUID(id) {
		return entity.ReportView{}, entity.ErrNotFound
	}

	r, err := uc.r.Reports.Claim(ctx, adminID, id)
	if err != nil {
		return entity.ReportView{}, fmt.Errorf("adminreports - Claim: %w", err)
	}

	return uc.view(ctx, r)
}

// Resolve closes the report (and the open ones on the same target when asked).
func (uc *UseCase) Resolve(ctx context.Context, adminID, id string, in usecase.ResolveInput) (entity.ReportView, int, error) {
	note, ok := trimmed(in.Note, entity.ResolveNoteMin, entity.ResolveNoteMax)

	details := []string{}
	if in.Status != entity.ReportResolved && in.Status != entity.ReportDismissed {
		details = append(details, "status", entity.DetailEnum)
	}

	if !ok {
		details = append(details, "note", entity.DetailLength)
	}

	if len(details) > 0 {
		return entity.ReportView{}, 0, entity.Reject(entity.ErrValidation, details...)
	}

	if !isUUID(id) {
		return entity.ReportView{}, 0, entity.ErrNotFound
	}

	r, closed, err := uc.r.Reports.Resolve(ctx, adminID, id,
		repo.Resolution{Status: in.Status, Note: note, ApplyToSameTarget: in.ApplyToSameTarget})
	if err != nil {
		return entity.ReportView{}, 0, fmt.Errorf("adminreports - Resolve: %w", err)
	}

	v, err := uc.view(ctx, r)

	return v, closed, err
}

func (uc *UseCase) view(ctx context.Context, r entity.Report) (entity.ReportView, error) {
	v, err := uc.views(ctx, []entity.Report{r})
	if err != nil {
		return entity.ReportView{}, err
	}

	return v[0], nil
}

// views names the people of each report and labels its target from the copies (§5.1).
func (uc *UseCase) views(ctx context.Context, rows []entity.Report) ([]entity.ReportView, error) {
	ids := repo.LookupIDs{}

	for _, r := range rows {
		ids.Users = append(ids.Users, r.ReporterID)
		ids.Users = appendRef(ids.Users, r.AssignedTo)
		ids.Users = appendRef(ids.Users, r.ResolvedBy)

		if r.TargetUserID != nil {
			ids.Users = append(ids.Users, *r.TargetUserID)
		}

		switch r.TargetType {
		case entity.TargetChannel:
			ids.Channels = append(ids.Channels, r.TargetID)
		case entity.TargetStream:
			ids.Streams = append(ids.Streams, r.TargetID)
		}
	}

	l, err := uc.r.Replica.Lookup(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("adminreports - lookup: %w", err)
	}

	out := make([]entity.ReportView, 0, len(rows))

	for _, r := range rows {
		r.Reporter = ref(l, r.ReporterID)
		r.AssignedTo = refPtr(l, r.AssignedTo)
		r.ResolvedBy = refPtr(l, r.ResolvedBy)
		out = append(out, entity.ReportView{Report: r, Target: target(l, r)})
	}

	return out, nil
}

func appendRef(ids []string, r *entity.Ref) []string {
	if r == nil {
		return ids
	}

	return append(ids, r.ID)
}

func ref(l repo.Lookup, id string) entity.Ref {
	return entity.Ref{ID: id, Username: l.Users[id].Username}
}

func refPtr(l repo.Lookup, r *entity.Ref) *entity.Ref {
	if r == nil {
		return nil
	}

	out := ref(l, r.ID)

	return &out
}

// target builds the label and the web link of a report's target.
func target(l repo.Lookup, r entity.Report) entity.Target {
	t := entity.Target{ID: r.TargetID, UserID: r.TargetUserID, Label: r.TargetID}

	switch r.TargetType {
	case entity.TargetUser:
		if u, ok := l.Users[r.TargetID]; ok {
			t.Label = "@" + u.Username
		}
	case entity.TargetChannel:
		if c, ok := l.Channels[r.TargetID]; ok {
			t.Label, t.Link = c.DisplayName, link("/"+c.Handle)
		}
	case entity.TargetStream:
		if s, ok := l.Streams[r.TargetID]; ok {
			t.Label = s.Title
		}

		t.Link = link("/live/" + r.TargetID)
	case entity.TargetMessage:
		t.Label = messageLabel(l, r)
		if r.StreamID != nil {
			t.Link = link("/live/" + *r.StreamID)
		}
	}

	return t
}

func messageLabel(l repo.Lookup, r entity.Report) string {
	author := "@?"

	if r.TargetUserID != nil {
		if u, ok := l.Users[*r.TargetUserID]; ok {
			author = "@" + u.Username
		}
	}

	for _, it := range r.Evidence {
		if it.MessageID == r.TargetID {
			return author + ": " + entity.Excerpt(it.Body, entity.LabelExcerptMax)
		}
	}

	return author
}

func link(s string) *string { return &s }
