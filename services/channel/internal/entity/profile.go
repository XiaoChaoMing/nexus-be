package entity

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Profile limits (NXL-D BR-D14…D16, NXL-00 §12).
const (
	BioMax         = 300
	SocialLinksMax = 5
	SocialURLMax   = 300
)

// SocialLink is one link on the channel's About tab.
type SocialLink struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// SocialTypes are the link kinds the web client has icons for.
func SocialTypes() []string {
	return []string{"YOUTUBE", "X", "FACEBOOK", "TIKTOK", "DISCORD", "WEBSITE"}
}

// _newHandleRe: a handle a creator picks is lower case (BR-D14); older handles may still contain upper case.
var _newHandleRe = regexp.MustCompile(`^[a-z0-9_]{3,25}$`)

// ReservedHandles collide with web routes (/{handle} is a channel page).
func ReservedHandles() []string {
	return []string{
		"login", "register", "settings", "studio", "admin", "live", "vod", "browse", "search", "following",
		"creator", "c", "media", "hls", "emotes", "api", "v1", "ws", "help", "about", "terms", "privacy",
		"notifications", "messages", "home", "categories", "schedule", "explore", "nexuslive",
	}
}

// CheckNewHandle returns "" when h can be chosen, else the VALIDATION_FAILED detail (FORMAT / INVALID).
func CheckNewHandle(h string) string {
	if !_newHandleRe.MatchString(h) {
		return DetailFormat
	}

	if slices.Contains(ReservedHandles(), h) {
		return DetailInvalid
	}

	return ""
}

// ProfilePatch is a partial update of the creator's channel; nil fields stay unchanged.
type ProfilePatch struct {
	DisplayName *string
	// Bio set with ClearBio false replaces it; ClearBio removes it.
	Bio           *string
	ClearBio      bool
	SocialLinks   *[]SocialLink
	MatureDefault *bool
	Handle        *string
}

// Empty reports a patch without any field.
func (p ProfilePatch) Empty() bool {
	return p.DisplayName == nil && p.Bio == nil && !p.ClearBio && p.SocialLinks == nil && p.MatureDefault == nil &&
		p.Handle == nil
}

// Normalize trims the text fields; an empty bio clears it.
func (p ProfilePatch) Normalize() ProfilePatch {
	if p.DisplayName != nil {
		v := strings.TrimSpace(*p.DisplayName)
		p.DisplayName = &v
	}

	if p.Bio != nil {
		v := strings.TrimSpace(*p.Bio)
		if v == "" {
			p.Bio, p.ClearBio = nil, true
		} else {
			p.Bio = &v
		}
	}

	if p.Handle != nil {
		v := strings.TrimSpace(*p.Handle)
		p.Handle = &v
	}

	if p.SocialLinks != nil {
		links := make([]SocialLink, 0, len(*p.SocialLinks))
		for _, l := range *p.SocialLinks {
			links = append(links, SocialLink{Type: strings.ToUpper(strings.TrimSpace(l.Type)), URL: strings.TrimSpace(l.URL)})
		}

		p.SocialLinks = &links
	}

	return p
}

// Validate returns the VALIDATION_FAILED details of a normalized patch (empty when valid).
func (p ProfilePatch) Validate() map[string]string {
	errs := map[string]string{}

	if p.Empty() {
		errs["body"] = DetailRequired
	}

	if p.DisplayName != nil && !lengthIn(*p.DisplayName, 1, DisplayNameMaxLen) {
		errs["displayName"] = DetailLength
	}

	if p.Bio != nil && !lengthIn(*p.Bio, 0, BioMax) {
		errs["bio"] = DetailLength
	}

	if p.SocialLinks != nil {
		if d := checkSocialLinks(*p.SocialLinks); d != "" {
			errs["socialLinks"] = d
		}
	}

	if p.Handle != nil {
		if d := CheckNewHandle(*p.Handle); d != "" {
			errs["handle"] = d
		}
	}

	return errs
}

func lengthIn(s string, lo, hi int) bool {
	n := utf8.RuneCountInString(s)

	return n >= lo && n <= hi
}

func checkSocialLinks(links []SocialLink) string {
	if len(links) > SocialLinksMax {
		return DetailMax
	}

	for _, l := range links {
		if !slices.Contains(SocialTypes(), l.Type) {
			return DetailEnum
		}

		if !validHTTPS(l.URL) {
			return DetailFormat
		}
	}

	return ""
}

func validHTTPS(s string) bool {
	if len(s) > SocialURLMax || !strings.HasPrefix(s, "https://") {
		return false
	}

	u, err := url.Parse(s)

	return err == nil && u.Host != "" && !strings.ContainsAny(s, " \t\r\n")
}

// HandleChange says whether a patch really changes the handle (case-insensitive) and, if so, whether it is allowed now.
func (c Channel) HandleChange(newHandle string, interval time.Duration, now time.Time) (changes bool, nextChangeAt *time.Time) {
	if strings.EqualFold(newHandle, c.Handle) {
		return false, nil
	}

	if c.HandleChangedAt != nil && interval > 0 {
		next := c.HandleChangedAt.Add(interval)
		if next.After(now) {
			return true, &next
		}
	}

	return true, nil
}

// NextHandleChangeAt is when the handle may change again (nil = now).
func (c Channel) NextHandleChangeAt(interval time.Duration, now time.Time) *time.Time {
	if c.HandleChangedAt == nil {
		return nil
	}

	next := c.HandleChangedAt.Add(interval)
	if !next.After(now) {
		return nil
	}

	return &next
}

// ProfileViewer is the signed-in caller's relation to a channel.
type ProfileViewer struct {
	Following   bool
	NotifyLevel *NotifyLevel
	IsOwner     bool
}

// Profile is the public channel page.
type Profile struct {
	Channel       Channel
	CurrentStream *LiveStream
	// Viewer is nil for guests.
	Viewer *ProfileViewer
}

// ImageKind -.
type ImageKind string

// Image kinds of a channel.
const (
	ImageAvatar ImageKind = "avatar"
	ImageBanner ImageKind = "banner"
)

// ImageSpec is the output size of an uploaded image and the smallest input accepted.
type ImageSpec struct {
	Width, Height       int
	MinWidth, MinHeight int
	MaxBytes            int
}

// Image output sizes (NXL-D §3.7).
const (
	AvatarSize      = 256
	AvatarMinSize   = 128
	BannerWidth     = 1600
	BannerHeight    = 500
	BannerMinWidth  = 800
	BannerMinHeight = 250
	// ImageMaxPixels refuses decompression bombs before decoding.
	ImageMaxPixels = 40_000_000
)

// Spec returns the output spec of kind with maxBytes as the upload limit.
func (k ImageKind) Spec(maxBytes int) ImageSpec {
	if k == ImageBanner {
		return ImageSpec{Width: BannerWidth, Height: BannerHeight, MinWidth: BannerMinWidth, MinHeight: BannerMinHeight, MaxBytes: maxBytes}
	}

	return ImageSpec{Width: AvatarSize, Height: AvatarSize, MinWidth: AvatarMinSize, MinHeight: AvatarMinSize, MaxBytes: maxBytes}
}
