package v1

import (
	"encoding/json"
	"time"

	"github.com/minhnn/nexuslive-be/services/safety/internal/entity"
)

// Data wraps every successful body.
type Data[T any] struct {
	Data T `json:"data"`
}

// ErrorResponse wraps every error body.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody -.
type ErrorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

// ReportRequest is POST /v1/reports.
type ReportRequest struct {
	TargetType string  `json:"targetType" enums:"USER,CHANNEL,STREAM,MESSAGE"`
	TargetID   string  `json:"targetId"`
	Reason     string  `json:"reason"     enums:"SPAM,HARASSMENT,VIOLENCE,SENSITIVE,COPYRIGHT,OTHER"`
	Note       *string `json:"note"`
	// StreamID is required for MESSAGE (the room of the message); optional for USER (evidence from that room).
	StreamID *string `json:"streamId"`
}

// ReportCreated -.
type ReportCreated struct {
	ID        string    `json:"id"`
	Status    string    `json:"status" enums:"OPEN,IN_REVIEW,RESOLVED,DISMISSED"`
	CreatedAt time.Time `json:"createdAt"`
}

// Ref names a person.
type Ref struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// Target is a report's target.
type Target struct {
	ID     string  `json:"id"`
	Label  string  `json:"label"`
	Link   *string `json:"link"`
	UserID *string `json:"userId"`
}

// AdminReport is a row of the admin queue.
type AdminReport struct {
	ID              string    `json:"id"`
	TargetType      string    `json:"targetType" enums:"USER,CHANNEL,STREAM,MESSAGE"`
	Target          Target    `json:"target"`
	Reason          string    `json:"reason"`
	Reporter        Ref       `json:"reporter"`
	Status          string    `json:"status" enums:"OPEN,IN_REVIEW,RESOLVED,DISMISSED"`
	AssignedTo      *Ref      `json:"assignedTo"`
	SameTargetCount int       `json:"sameTargetCount"`
	CreatedAt       time.Time `json:"createdAt"`
}

// EvidenceItem is a chat message captured when the report was filed; username / displayName come from the
// user copy (null when the copy does not have the author).
type EvidenceItem struct {
	MessageID   string          `json:"messageId"`
	UserID      string          `json:"userId"`
	Username    *string         `json:"username"`
	DisplayName *string         `json:"displayName"`
	Body        string          `json:"body"`
	Fragments   json.RawMessage `json:"fragments" swaggertype:"array,object"`
	CreatedAt   time.Time       `json:"createdAt"`
	Deleted     bool            `json:"deleted"`
}

// AdminAction is an admin action, or a chat timeout / ban of the reported user (CHAT_* types, read from the
// audit log): admin is then the moderator, actorRole OWNER, MODERATOR or ADMIN and channelId the channel.
type AdminAction struct {
	ID         string     `json:"id"`
	Type       string     `json:"type" enums:"LOCK_USER,UNLOCK_USER,SUSPEND_CHANNEL,RESTORE_CHANNEL,TAKEDOWN_STREAM,CHAT_TIMEOUT,CHAT_UNTIMEOUT,CHAT_BAN,CHAT_UNBAN"`
	TargetType string     `json:"targetType" enums:"USER,CHANNEL,STREAM"`
	TargetID   string     `json:"targetId"`
	Reason     string     `json:"reason"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	Admin      Ref        `json:"admin"`
	ActorRole  string     `json:"actorRole" enums:"ADMIN,OWNER,MODERATOR"`
	ChannelID  *string    `json:"channelId"`
	ReportID   *string    `json:"reportId"`
	CreatedAt  time.Time  `json:"createdAt"`
}

// AdminReportDetail -.
type AdminReportDetail struct {
	AdminReport

	Note     *string `json:"note"`
	StreamID *string `json:"streamId"`
	// ChannelID is the reported channel, or the channel of the reported stream / of the room of the report.
	ChannelID      *string        `json:"channelId"`
	Evidence       []EvidenceItem `json:"evidence"`
	EvidenceStatus string         `json:"evidenceStatus" enums:"CAPTURED,NONE,UNAVAILABLE"`
	ResolvedBy     *Ref           `json:"resolvedBy"`
	ResolvedAt     *time.Time     `json:"resolvedAt"`
	ResolutionNote *string        `json:"resolutionNote"`
	RelatedReports []AdminReport  `json:"relatedReports"`
	Actions        []AdminAction  `json:"actions"`
}

// ReportList -.
type ReportList struct {
	Items      []AdminReport `json:"items"`
	NextCursor *string       `json:"nextCursor"`
}

// OpenCount is the admin menu badge.
type OpenCount struct {
	Open     int `json:"open"`
	InReview int `json:"inReview"`
}

// ResolveRequest -.
type ResolveRequest struct {
	Status            string `json:"status" enums:"RESOLVED,DISMISSED"`
	Note              string `json:"note"`
	ApplyToSameTarget bool   `json:"applyToSameTarget"`
}

// Resolved -.
type Resolved struct {
	Report      AdminReport `json:"report"`
	ClosedCount int         `json:"closedCount"`
}

// LockRequest -; durationHours null locks until an admin unlocks.
type LockRequest struct {
	DurationHours *int    `json:"durationHours" enums:"24,168,720"`
	Reason        string  `json:"reason"`
	ReportID      *string `json:"reportId"`
}

// ActionRequest is the body of unlock, suspend, restore and takedown (reportId ignored by unlock / restore).
type ActionRequest struct {
	Reason   string  `json:"reason"`
	ReportID *string `json:"reportId"`
}

// ActionAccepted -.
type ActionAccepted struct {
	ActionID string `json:"actionId"`
}

// AdminUserRow -.
type AdminUserRow struct {
	ID          string     `json:"id"`
	Username    string     `json:"username"`
	DisplayName string     `json:"displayName"`
	GlobalRole  string     `json:"globalRole" enums:"USER,ADMIN"`
	Status      string     `json:"status" enums:"ACTIVE,LOCKED,DELETED"`
	LockedUntil *time.Time `json:"lockedUntil"`
	OpenReports int        `json:"openReports"`
}

// UserList -.
type UserList struct {
	Items      []AdminUserRow `json:"items"`
	NextCursor *string        `json:"nextCursor"`
}

// AdminChannel -.
type AdminChannel struct {
	ID              string     `json:"id"`
	Handle          string     `json:"handle"`
	DisplayName     string     `json:"displayName"`
	AvatarURL       *string    `json:"avatarUrl"`
	Owner           Ref        `json:"owner"`
	Status          string     `json:"status" enums:"ACTIVE,SUSPENDED"`
	SuspendedAt     *time.Time `json:"suspendedAt"`
	CurrentStreamID *string    `json:"currentStreamId"`
	OpenReports     int        `json:"openReports"`
}

// ChannelList -.
type ChannelList struct {
	Items      []AdminChannel `json:"items"`
	NextCursor *string        `json:"nextCursor"`
}

// AuditEntry -.
type AuditEntry struct {
	ID            string         `json:"id"`
	Actor         *Ref           `json:"actor"`
	ActorRole     string         `json:"actorRole"`
	Action        string         `json:"action"`
	ResourceType  string         `json:"resourceType"`
	ResourceID    *string        `json:"resourceId"`
	Reason        *string        `json:"reason"`
	SourceService string         `json:"sourceService"`
	Metadata      map[string]any `json:"metadata"`
	CreatedAt     time.Time      `json:"createdAt"`
}

// AuditList -.
type AuditList struct {
	Items      []AuditEntry `json:"items"`
	NextCursor *string      `json:"nextCursor"`
}

func refOf(r entity.Ref) Ref { return Ref{ID: r.ID, Username: r.Username} }

func refPtr(r *entity.Ref) *Ref {
	if r == nil {
		return nil
	}

	out := refOf(*r)

	return &out
}

func adminReport(v entity.ReportView) AdminReport {
	return AdminReport{
		ID: v.ID, TargetType: v.TargetType,
		Target: Target{ID: v.Target.ID, Label: v.Target.Label, Link: v.Target.Link, UserID: v.Target.UserID},
		Reason: v.Reason, Reporter: refOf(v.Reporter), Status: v.Status, AssignedTo: refPtr(v.AssignedTo),
		SameTargetCount: v.SameTarget, CreatedAt: v.CreatedAt,
	}
}

func adminReports(views []entity.ReportView) []AdminReport {
	out := make([]AdminReport, 0, len(views))
	for _, v := range views {
		out = append(out, adminReport(v))
	}

	return out
}

func reportDetail(d entity.ReportDetail) AdminReportDetail {
	ev := make([]EvidenceItem, 0, len(d.Evidence))
	for _, e := range d.Evidence {
		frags := e.Fragments
		if len(frags) == 0 {
			frags = json.RawMessage("[]")
		}

		item := EvidenceItem{
			MessageID: e.MessageID, UserID: e.UserID, Body: e.Body, Fragments: frags,
			CreatedAt: e.CreatedAt, Deleted: e.Deleted,
		}

		if u, ok := d.Authors[e.UserID]; ok {
			item.Username, item.DisplayName = &u.Username, &u.DisplayName
		}

		ev = append(ev, item)
	}

	actions := make([]AdminAction, 0, len(d.Actions))
	for _, a := range d.Actions {
		actions = append(actions, AdminAction{
			ID: a.ID, Type: a.Type, TargetType: a.TargetType, TargetID: a.TargetID, Reason: a.Reason, ExpiresAt: a.ExpiresAt,
			Admin: refOf(a.Admin), ActorRole: a.ActorRole, ChannelID: a.ChannelID, ReportID: a.ReportID, CreatedAt: a.CreatedAt,
		})
	}

	return AdminReportDetail{
		AdminReport: adminReport(d.ReportView), Note: d.Note, StreamID: d.StreamID, ChannelID: d.ChannelID, Evidence: ev,
		EvidenceStatus: d.EvidenceStatus, ResolvedBy: refPtr(d.ResolvedBy), ResolvedAt: d.ResolvedAt,
		ResolutionNote: d.ResolutionNote, RelatedReports: adminReports(d.Related), Actions: actions,
	}
}

func userRow(u entity.UserCopy) AdminUserRow {
	return AdminUserRow{
		ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, GlobalRole: u.GlobalRole,
		Status: u.Status, LockedUntil: u.LockedUntil, OpenReports: u.OpenReports,
	}
}

func adminChannel(c entity.ChannelCopy) AdminChannel {
	return AdminChannel{
		ID: c.ID, Handle: c.Handle, DisplayName: c.DisplayName, AvatarURL: c.AvatarURL,
		Owner: refOf(c.Owner), Status: c.Status, SuspendedAt: c.SuspendedAt, CurrentStreamID: c.CurrentStreamID,
		OpenReports: c.OpenReports,
	}
}

func auditEntry(e entity.AuditEntry) AuditEntry {
	return AuditEntry{
		ID: e.ID, Actor: refPtr(e.Actor), ActorRole: e.ActorRole, Action: e.Action,
		ResourceType: e.ResourceType, ResourceID: e.ResourceID, Reason: e.Reason, SourceService: e.SourceService,
		Metadata: e.Metadata, CreatedAt: e.CreatedAt,
	}
}

// mapItems converts a page of entities.
func mapItems[T, R any](in []T, f func(T) R) []R {
	out := make([]R, 0, len(in))
	for _, v := range in {
		out = append(out, f(v))
	}

	return out
}

func cursor(next string) *string {
	if next == "" {
		return nil
	}

	return &next
}
