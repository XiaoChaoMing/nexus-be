// Package response holds the JSON bodies of channel-svc's public REST API (camelCase, absent values are null).
package response

import (
	"time"

	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
)

// Data wraps every successful body.
type Data[T any] struct {
	Data T `json:"data"`
}

// Error wraps every error body.
type Error struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody -.
type ErrorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

// ChannelCard is the short form of a channel.
type ChannelCard struct {
	ID          string  `json:"id"`
	Handle      string  `json:"handle"`
	DisplayName string  `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl"`
	Verified    bool    `json:"verified"`
}

// CategoryRef -.
type CategoryRef struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// Category -.
type Category struct {
	ID        string   `json:"id"`
	Slug      string   `json:"slug"`
	Name      string   `json:"name"`
	Group     string   `json:"group" enums:"GAMES,ESPORTS,MUSIC,IRL,CREATIVE"`
	PosterURL *string  `json:"posterUrl"`
	Tags      []string `json:"tags"`
	Position  int      `json:"position"`
	// ViewerCount is the sum over the category's live streams; null when counts are unavailable.
	ViewerCount *int `json:"viewerCount"`
}

// CategoryList -.
type CategoryList struct {
	Items      []Category `json:"items"`
	NextCursor *string    `json:"nextCursor"`
}

// LiveStreamRef is the stream a channel has on air.
type LiveStreamRef struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Category    *CategoryRef `json:"category"`
	ViewerCount *int         `json:"viewerCount"`
	IsMature    bool         `json:"isMature"`
	StartedAt   time.Time    `json:"startedAt"`
}

// SocialLink -.
type SocialLink struct {
	Type string `json:"type" enums:"YOUTUBE,X,FACEBOOK,TIKTOK,DISCORD,WEBSITE"`
	URL  string `json:"url"`
}

// ChannelBase is what ChannelProfile and StudioChannel share.
type ChannelBase struct {
	ID              string         `json:"id"`
	Handle          string         `json:"handle"`
	DisplayName     string         `json:"displayName"`
	Bio             *string        `json:"bio"`
	AvatarURL       *string        `json:"avatarUrl"`
	BannerURL       *string        `json:"bannerUrl"`
	SocialLinks     []SocialLink   `json:"socialLinks"`
	Verified        bool           `json:"verified"`
	MatureDefault   bool           `json:"matureDefault"`
	Status          string         `json:"status"`
	FollowerCount   int            `json:"followerCount"`
	IsLive          bool           `json:"isLive"`
	CurrentStreamID *string        `json:"currentStreamId"`
	CurrentStream   *LiveStreamRef `json:"currentStream"`
	LastLiveAt      *time.Time     `json:"lastLiveAt"`
	CreatedAt       time.Time      `json:"createdAt"`
}

// ProfileViewer is the signed-in caller's relation to the channel.
type ProfileViewer struct {
	Following   bool    `json:"following"`
	NotifyLevel *string `json:"notifyLevel" enums:"ALL,LIVE_ONLY,OFF"`
	IsOwner     bool    `json:"isOwner"`
}

// ChannelProfile is the public channel page.
type ChannelProfile struct {
	ChannelBase
	// Viewer is null for guests.
	Viewer *ProfileViewer `json:"viewer"`
}

// StudioChannel is the creator's own channel in the Studio.
type StudioChannel struct {
	ChannelBase
	HandleChangedAt    *time.Time `json:"handleChangedAt"`
	NextHandleChangeAt *time.Time `json:"nextHandleChangeAt"`
}

// FollowState -.
type FollowState struct {
	Following   bool      `json:"following"`
	NotifyLevel string    `json:"notifyLevel" enums:"ALL,LIVE_ONLY,OFF"`
	FollowedAt  time.Time `json:"followedAt"`
}

// FollowedChannel -.
type FollowedChannel struct {
	Channel       ChannelCard    `json:"channel"`
	IsLive        bool           `json:"isLive"`
	CurrentStream *LiveStreamRef `json:"currentStream"`
	LastLiveAt    *time.Time     `json:"lastLiveAt"`
	NotifyLevel   string         `json:"notifyLevel" enums:"ALL,LIVE_ONLY,OFF"`
	FollowedAt    time.Time      `json:"followedAt"`
}

// FollowingList -.
type FollowingList struct {
	Items      []FollowedChannel `json:"items"`
	NextCursor *string           `json:"nextCursor"`
}

// Sidebar -.
type Sidebar struct {
	Live           []FollowedChannel `json:"live"`
	Offline        []FollowedChannel `json:"offline"`
	TotalFollowing int               `json:"totalFollowing"`
}

// Occurrence is one broadcast on a channel's Schedule tab.
type Occurrence struct {
	ScheduleID   string       `json:"scheduleId"`
	OccurrenceAt time.Time    `json:"occurrenceAt"`
	Title        string       `json:"title"`
	Category     *CategoryRef `json:"category"`
	DurationMin  int          `json:"durationMin"`
	// StreamID is set once stream-svc created the SCHEDULED stream (≤ 24 h before): link to /live/{streamId}.
	StreamID *string `json:"streamId"`
	// Reminded is null for guests.
	Reminded *bool `json:"reminded"`
}

// OccurrenceList -.
type OccurrenceList struct {
	Items      []Occurrence `json:"items"`
	NextCursor *string      `json:"nextCursor"`
}

// NextOccurrence -.
type NextOccurrence struct {
	OccurrenceAt  time.Time `json:"occurrenceAt"`
	ReminderCount int       `json:"reminderCount"`
	StreamID      *string   `json:"streamId"`
}

// Schedule -.
type Schedule struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Category    *CategoryRef `json:"category"`
	StartAt     time.Time    `json:"startAt"`
	DurationMin int          `json:"durationMin"`
	// RRule is null for a one-off, else FREQ=WEEKLY;BYDAY=…[;COUNT=n|;UNTIL=yyyymmddThhmmssZ].
	RRule           *string          `json:"rrule"`
	Timezone        string           `json:"timezone"`
	Status          string           `json:"status" enums:"ACTIVE,CANCELLED"`
	ParentID        *string          `json:"parentId"`
	NextOccurrences []NextOccurrence `json:"nextOccurrences"`
	CreatedAt       time.Time        `json:"createdAt"`
	UpdatedAt       time.Time        `json:"updatedAt"`
}

// ScheduleList -.
type ScheduleList struct {
	Items      []Schedule `json:"items"`
	NextCursor *string    `json:"nextCursor"`
}

// MovedOccurrence is the one-off schedule a moved occurrence became.
type MovedOccurrence struct {
	Schedule Schedule `json:"schedule"`
}

// Reminder -.
type Reminder struct {
	ScheduleID   string    `json:"scheduleId"`
	OccurrenceAt time.Time `json:"occurrenceAt"`
	CreatedAt    time.Time `json:"createdAt"`
}

// SearchItem is one hit; target is the web route it opens.
type SearchItem struct {
	Type     string  `json:"type" enums:"channel,stream,category,vod"`
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Subtitle *string `json:"subtitle"`
	ImageURL *string `json:"imageUrl"`
	// ViewerCount: live viewers of a live channel or stream, the category total; null otherwise.
	ViewerCount *int   `json:"viewerCount"`
	IsLive      bool   `json:"isLive"`
	IsMature    bool   `json:"isMature"`
	Target      string `json:"target"`
}

// SearchGroup -.
type SearchGroup struct {
	Type  string       `json:"type" enums:"channel,stream,category,vod"`
	Items []SearchItem `json:"items"`
}

// Suggestions -.
type Suggestions struct {
	Query  string        `json:"query"`
	Groups []SearchGroup `json:"groups"`
}

// SearchList -.
type SearchList struct {
	Items      []SearchItem `json:"items"`
	NextCursor *string      `json:"nextCursor"`
}

// SearchQuery is one entry of the search history.
type SearchQuery struct {
	ID        string    `json:"id"`
	Query     string    `json:"query"`
	CreatedAt time.Time `json:"createdAt"`
}

// SearchHistory -.
type SearchHistory struct {
	Items      []SearchQuery `json:"items"`
	NextCursor *string       `json:"nextCursor"`
}

// ImageURL -.
type ImageURL struct {
	URL string `json:"url"`
}

// Upcoming is the channel's next occurrence.
type Upcoming struct {
	ScheduleID    string    `json:"scheduleId"`
	OccurrenceAt  time.Time `json:"occurrenceAt"`
	Title         string    `json:"title"`
	ReminderCount int       `json:"reminderCount"`
}

// ChannelStats is channel-svc's half of the Studio dashboard.
type ChannelStats struct {
	Range         string    `json:"range" enums:"7d,28d"`
	FollowerCount int       `json:"followerCount"`
	NewFollowers  int       `json:"newFollowers"`
	Upcoming      *Upcoming `json:"upcoming"`
}

// ---- mapping --------------------------------------------------------------------------------------

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}

	u := t.UTC()

	return &u
}

// CategoryRefOf -.
func CategoryRefOf(c *entity.Category) *CategoryRef {
	if c == nil {
		return nil
	}

	return &CategoryRef{ID: c.ID, Slug: c.Slug, Name: c.Name}
}

// CategoryOf -.
func CategoryOf(c entity.Category) Category {
	tags := c.Tags
	if tags == nil {
		tags = []string{}
	}

	return Category{
		ID: c.ID, Slug: c.Slug, Name: c.Name, Group: string(c.Group), PosterURL: c.PosterURL, Tags: tags,
		Position: c.Position, ViewerCount: c.ViewerCount,
	}
}

// LiveOf -.
func LiveOf(s *entity.LiveStream) *LiveStreamRef {
	if s == nil {
		return nil
	}

	return &LiveStreamRef{
		ID: s.StreamID, Title: s.Title, Category: CategoryRefOf(s.Category), ViewerCount: s.ViewerCount,
		IsMature: s.IsMature, StartedAt: s.StartedAt.UTC(),
	}
}

// CardOf -.
func CardOf(c entity.Channel) ChannelCard {
	return ChannelCard{ID: c.ID, Handle: c.Handle, DisplayName: c.DisplayName, AvatarURL: c.AvatarURL, Verified: c.Verified}
}

// BaseOf -.
func BaseOf(c entity.Channel, live *entity.LiveStream) ChannelBase {
	links := make([]SocialLink, 0, len(c.SocialLinks))
	for _, l := range c.SocialLinks {
		links = append(links, SocialLink(l))
	}

	return ChannelBase{
		ID: c.ID, Handle: c.Handle, DisplayName: c.DisplayName, Bio: c.Bio, AvatarURL: c.AvatarURL, BannerURL: c.BannerURL,
		SocialLinks: links, Verified: c.Verified, MatureDefault: c.MatureDefault, Status: string(c.Status),
		FollowerCount: c.FollowerCount, IsLive: c.IsLive, CurrentStreamID: c.CurrentStreamID, CurrentStream: LiveOf(live),
		LastLiveAt: utcPtr(c.LastLiveAt), CreatedAt: c.CreatedAt.UTC(),
	}
}

// ProfileOf -.
func ProfileOf(p entity.Profile) ChannelProfile {
	out := ChannelProfile{ChannelBase: BaseOf(p.Channel, p.CurrentStream)}

	if p.Viewer != nil {
		out.Viewer = &ProfileViewer{Following: p.Viewer.Following, IsOwner: p.Viewer.IsOwner}
		if p.Viewer.NotifyLevel != nil {
			l := string(*p.Viewer.NotifyLevel)
			out.Viewer.NotifyLevel = &l
		}
	}

	return out
}

// StudioChannelOf -.
func StudioChannelOf(c entity.Channel, nextHandleChangeAt *time.Time) StudioChannel {
	return StudioChannel{
		ChannelBase: BaseOf(c, nil), HandleChangedAt: utcPtr(c.HandleChangedAt),
		NextHandleChangeAt: utcPtr(nextHandleChangeAt),
	}
}

// FollowStateOf -.
func FollowStateOf(f entity.Follow) FollowState {
	return FollowState{Following: true, NotifyLevel: string(f.NotifyLevel), FollowedAt: f.CreatedAt.UTC()}
}

// FollowedOf -.
func FollowedOf(items []entity.FollowedChannel) []FollowedChannel {
	out := make([]FollowedChannel, 0, len(items))

	for _, f := range items {
		out = append(out, FollowedChannel{
			Channel: CardOf(f.Channel), IsLive: f.Channel.IsLive, CurrentStream: LiveOf(f.CurrentStream),
			LastLiveAt: utcPtr(f.Channel.LastLiveAt), NotifyLevel: string(f.NotifyLevel), FollowedAt: f.FollowedAt.UTC(),
		})
	}

	return out
}

// OccurrenceOf -.
func OccurrenceOf(o entity.OccurrenceView) Occurrence {
	return Occurrence{
		ScheduleID: o.ScheduleID, OccurrenceAt: o.OccurrenceAt.UTC(), Title: o.Title,
		Category: CategoryRefOf(o.Category), DurationMin: o.DurationMin, StreamID: o.StreamID, Reminded: o.Reminded,
	}
}

// ScheduleOf -.
func ScheduleOf(v entity.ScheduleView) Schedule {
	s := v.Schedule
	out := Schedule{
		ID: s.ID, Title: s.Title, Category: CategoryRefOf(v.Category), StartAt: s.StartAt.UTC(), DurationMin: s.DurationMin,
		Timezone: s.Timezone, Status: string(s.Status), ParentID: s.ParentID, NextOccurrences: []NextOccurrence{},
		CreatedAt: s.CreatedAt.UTC(), UpdatedAt: s.UpdatedAt.UTC(),
	}

	if s.RRule != nil {
		r := s.RRule.String()
		out.RRule = &r
	}

	for _, n := range v.Next {
		out.NextOccurrences = append(out.NextOccurrences, NextOccurrence{
			OccurrenceAt: n.At.UTC(), ReminderCount: n.ReminderCount,
			StreamID: n.StreamID,
		})
	}

	return out
}

// SearchItemOf -.
func SearchItemOf(d entity.SearchDoc) SearchItem {
	out := SearchItem{
		Type: string(d.Type), ID: d.ID, Title: d.Title, Subtitle: d.Subtitle, ImageURL: d.ImageURL,
		IsLive: d.IsLive, IsMature: d.IsMature, Target: d.Target,
	}

	if d.Type == entity.DocCategory || (d.IsLive && d.Type != entity.DocVOD) {
		n := d.Viewers
		out.ViewerCount = &n
	}

	return out
}

// SearchItemsOf -.
func SearchItemsOf(docs []entity.SearchDoc) []SearchItem {
	out := make([]SearchItem, 0, len(docs))
	for _, d := range docs {
		out = append(out, SearchItemOf(d))
	}

	return out
}

// Cursor is nil for an empty next cursor.
func Cursor(s string) *string {
	if s == "" {
		return nil
	}

	return &s
}
