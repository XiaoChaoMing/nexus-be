package entity

import (
	"regexp"
	"slices"
	"time"
)

// Realtime events of the overlay:{channelId} topic.
const (
	// EventOverlaySettings carries the saved OverlaySettings so an open OBS page restyles itself.
	EventOverlaySettings = "overlay_settings"
	// EventOverlayTestGift is a gift alert the owner asked for from Studio; it never reaches a chat room.
	EventOverlayTestGift = "overlay_test_gift"
)

// Overlay chat presets, backgrounds, fonts and username coloring.
const (
	OverlayPresetGlass   = "GLASS"
	OverlayPresetDark    = "DARK"
	OverlayPresetNeon    = "NEON"
	OverlayPresetMinimal = "MINIMAL"

	OverlayBackgroundTransparent = "TRANSPARENT"
	OverlayBackgroundSolid       = "SOLID"

	OverlayFontInter   = "INTER"
	OverlayFontSystem  = "SYSTEM"
	OverlayFontMono    = "MONO"
	OverlayFontRounded = "ROUNDED"

	OverlayUsernamePalette = "PALETTE"
	OverlayUsernameUniform = "UNIFORM"
)

// Gift alert placement and entrance animation.
const (
	OverlayPositionTop    = "TOP"
	OverlayPositionCenter = "CENTER"
	OverlayPositionBottom = "BOTTOM"

	OverlayAnimationSlide = "SLIDE"
	OverlayAnimationPop   = "POP"
	OverlayAnimationFade  = "FADE"
)

// Bounds of the numeric fields.
const (
	OverlayMinFontSize    = 12
	OverlayMaxFontSize    = 32
	OverlayMinMessages    = 5
	OverlayMaxMessages    = 30
	OverlayMinFadeSec     = 5
	OverlayMaxFadeSec     = 120
	OverlayMaxOpacity     = 100
	OverlayMinDurationSec = 3
	OverlayMaxDurationSec = 15
	OverlayMaxMinTotal    = 100_000_000
)

var hexColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// OverlayChat is the look of the chat overlay.
type OverlayChat struct {
	Preset        string `json:"preset"`
	Background    string `json:"background"`
	BgColor       string `json:"bgColor"`
	BgOpacity     int    `json:"bgOpacity"`
	TextColor     string `json:"textColor"`
	FontFamily    string `json:"fontFamily"`
	FontSize      int    `json:"fontSize"`
	ShowBadges    bool   `json:"showBadges"`
	UsernameColor string `json:"usernameColor"`
	MaxMessages   int    `json:"maxMessages"`
	// FadeAfterSec hides a message after that many seconds; 0 keeps it until it scrolls out.
	FadeAfterSec int `json:"fadeAfterSec"`
}

// OverlayAlert is the look and the filter of the gift alert overlay.
type OverlayAlert struct {
	AccentColor string `json:"accentColor"`
	DurationSec int    `json:"durationSec"`
	Position    string `json:"position"`
	Animation   string `json:"animation"`
	// MinTotal hides gifts worth less than this many đồng; 0 shows all.
	MinTotal    int64 `json:"minTotal"`
	ShowMessage bool  `json:"showMessage"`
}

// OverlaySettings are a channel's OBS overlay options. UpdatedAt is nil for a channel that never saved any.
type OverlaySettings struct {
	ChannelID string       `json:"-"`
	Chat      OverlayChat  `json:"chat"`
	Alert     OverlayAlert `json:"alert"`
	UpdatedBy *string      `json:"-"`
	UpdatedAt *time.Time   `json:"updatedAt"`
}

const (
	_defaultOverlayBgOpacity   = 40
	_defaultOverlayFontSize    = 18
	_defaultOverlayMaxMessages = 12
	_defaultAlertDurationSec   = 8
)

// DefaultOverlaySettings is what a channel that never saved anything gets.
func DefaultOverlaySettings(channelID string) OverlaySettings {
	return OverlaySettings{
		ChannelID: channelID,
		Chat: OverlayChat{
			Preset: OverlayPresetGlass, Background: OverlayBackgroundTransparent, BgColor: "#000000", BgOpacity: _defaultOverlayBgOpacity,
			TextColor: "#FFFFFF", FontFamily: OverlayFontInter, FontSize: _defaultOverlayFontSize, ShowBadges: true,
			UsernameColor: OverlayUsernamePalette, MaxMessages: _defaultOverlayMaxMessages, FadeAfterSec: 0,
		},
		Alert: OverlayAlert{
			AccentColor: "#8B5CF6", DurationSec: _defaultAlertDurationSec, Position: OverlayPositionTop, Animation: OverlayAnimationSlide,
			MinTotal: 0, ShowMessage: true,
		},
	}
}

// Validate checks every field; the details name each wrong one as "chat.fontSize" etc.
func (s OverlaySettings) Validate() error {
	var d []string

	d = s.Chat.collect(d)
	d = s.Alert.collect(d)

	if len(d) > 0 {
		return Reject(ErrValidation, d...)
	}

	return nil
}

func (c OverlayChat) collect(d []string) []string {
	d = enumField(d, "chat.preset", c.Preset, OverlayPresetGlass, OverlayPresetDark, OverlayPresetNeon, OverlayPresetMinimal)
	d = enumField(d, "chat.background", c.Background, OverlayBackgroundTransparent, OverlayBackgroundSolid)
	d = enumField(d, "chat.fontFamily", c.FontFamily, OverlayFontInter, OverlayFontSystem, OverlayFontMono, OverlayFontRounded)
	d = enumField(d, "chat.usernameColor", c.UsernameColor, OverlayUsernamePalette, OverlayUsernameUniform)
	d = colorField(d, "chat.bgColor", c.BgColor)
	d = colorField(d, "chat.textColor", c.TextColor)
	d = rangeField(d, "chat.bgOpacity", int64(c.BgOpacity), 0, OverlayMaxOpacity)
	d = rangeField(d, "chat.fontSize", int64(c.FontSize), OverlayMinFontSize, OverlayMaxFontSize)
	d = rangeField(d, "chat.maxMessages", int64(c.MaxMessages), OverlayMinMessages, OverlayMaxMessages)

	if c.FadeAfterSec != 0 {
		d = rangeField(d, "chat.fadeAfterSec", int64(c.FadeAfterSec), OverlayMinFadeSec, OverlayMaxFadeSec)
	}

	return d
}

func (a OverlayAlert) collect(d []string) []string {
	d = colorField(d, "alert.accentColor", a.AccentColor)
	d = enumField(d, "alert.position", a.Position, OverlayPositionTop, OverlayPositionCenter, OverlayPositionBottom)
	d = enumField(d, "alert.animation", a.Animation, OverlayAnimationSlide, OverlayAnimationPop, OverlayAnimationFade)
	d = rangeField(d, "alert.durationSec", int64(a.DurationSec), OverlayMinDurationSec, OverlayMaxDurationSec)

	return rangeField(d, "alert.minTotal", a.MinTotal, 0, OverlayMaxMinTotal)
}

func enumField(d []string, name, v string, allowed ...string) []string {
	if slices.Contains(allowed, v) {
		return d
	}

	return append(d, name, DetailEnum)
}

func colorField(d []string, name, v string) []string {
	if hexColor.MatchString(v) {
		return d
	}

	return append(d, name, DetailFormat)
}

func rangeField(d []string, name string, v, lo, hi int64) []string {
	if v >= lo && v <= hi {
		return d
	}

	return append(d, name, DetailLength)
}
