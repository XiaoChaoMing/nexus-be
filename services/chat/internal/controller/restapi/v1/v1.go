// Package v1 is the public REST API of chat-svc.
package v1

import (
	"errors"
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/chat/internal/controller/restapi/middleware"
	"github.com/minhnn/nexuslive-be/services/chat/internal/controller/restapi/v1/request"
	"github.com/minhnn/nexuslive-be/services/chat/internal/controller/restapi/v1/response"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
)

type routes struct {
	chat    usecase.Chat
	mod     usecase.Moderation
	roles   usecase.Roles
	overlay usecase.Overlay
	l       logger.Interface
}

// UseCases groups what the routes call.
type UseCases struct {
	Chat       usecase.Chat
	Moderation usecase.Moderation
	Roles      usecase.Roles
	Overlay    usecase.Overlay
}

// Auth holds the two authentication middlewares: Required rejects guests, Optional identifies callers when a
// token is sent.
type Auth struct {
	Required fiber.Handler
	Optional fiber.Handler
}

// NewRoutes registers /v1/chat/*, the chat routes of /v1/channels/{channelId} and /v1/me/channel-roles.
func NewRoutes(group fiber.Router, uc UseCases, auth Auth, l logger.Interface) {
	r := &routes{chat: uc.Chat, mod: uc.Moderation, roles: uc.Roles, overlay: uc.Overlay, l: l}

	group.Get("/chat/rooms/:streamId/messages", r.history)
	group.Delete("/chat/rooms/:streamId/messages/:messageId", auth.Required, r.deleteMessage)
	group.Put("/chat/rooms/:streamId/pin", auth.Required, r.pin)
	group.Delete("/chat/rooms/:streamId/pin", auth.Required, r.unpin)
	group.Get("/chat/emotes", r.emotes)

	ch := group.Group("/channels/:channelId")
	ch.Get("/chat-settings", r.settings)
	ch.Put("/chat-settings", auth.Required, r.updateSettings)
	ch.Get("/overlay-settings", r.overlaySettings)
	ch.Put("/overlay-settings", auth.Required, r.updateOverlaySettings)
	ch.Post("/overlay-test", auth.Required, r.testOverlay)
	ch.Put("/timeouts/:userId", auth.Required, r.timeout)
	ch.Delete("/timeouts/:userId", auth.Required, r.untimeout)
	ch.Put("/bans/:userId", auth.Required, r.ban)
	ch.Delete("/bans/:userId", auth.Required, r.unban)
	ch.Get("/restrictions", auth.Required, r.restrictions)
	ch.Get("/users/:userId/card", auth.Optional, r.card)
	ch.Get("/blocked-terms", auth.Required, r.terms)
	ch.Post("/blocked-terms", auth.Required, r.addTerm)
	ch.Delete("/blocked-terms/:termId", auth.Required, r.removeTerm)
	ch.Get("/roles", auth.Required, r.listRoles)
	ch.Post("/roles", auth.Required, r.assignRole)
	ch.Put("/roles/:userId", auth.Required, r.setRole)
	ch.Delete("/roles/:userId", auth.Required, r.revokeRole)

	group.Get("/me/channel-roles", auth.Required, r.myChannelRoles)
}

func errorJSON(ctx *fiber.Ctx, status int, code, message string, details map[string]string) error {
	return ctx.Status(status).JSON(response.Error{Error: response.ErrorBody{Code: code, Message: message, Details: details}})
}

func validationFailed(ctx *fiber.Ctx, details map[string]string) error {
	return errorJSON(ctx, http.StatusBadRequest, entity.CodeValidationFailed, "invalid request", details)
}

// statusOf is the HTTP status of each code that a REST route can return (NXL-00 §5).
func statusOf(code string) int {
	switch code {
	case entity.CodeValidationFailed:
		return http.StatusBadRequest
	case entity.CodeUnauthorized:
		return http.StatusUnauthorized
	case entity.CodeForbidden:
		return http.StatusForbidden
	case entity.CodeNotFound:
		return http.StatusNotFound
	case entity.CodeInvalidState, entity.CodeConflict, entity.CodeChatReadOnly:
		return http.StatusConflict
	case entity.CodeContentBlocked:
		return http.StatusUnprocessableEntity
	case entity.CodeRateLimited:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}

// fail maps a use-case error; unexpected errors are logged and answered with a generic message.
func (r *routes) fail(ctx *fiber.Ctx, op string, err error) error {
	code := entity.CodeOf(err)
	if code == entity.CodeInternal {
		r.l.Error("restapi - v1 - %s: %v", op, err)

		return errorJSON(ctx, http.StatusInternalServerError, code, "internal server error", nil)
	}

	return errorJSON(ctx, statusOf(code), code, rootMessage(err), entity.DetailsOf(err))
}

func rootMessage(err error) string {
	for {
		next := errors.Unwrap(err)
		if next == nil {
			return err.Error()
		}

		err = next
	}
}

// history godoc
//
//	@Summary     Chat history of a stream, newest first
//	@Description The items have the shape of the WebSocket `message` event. Pass nextCursor as `before` for older messages.
//	@Tags        chat
//	@Produce     json
//	@Param       streamId path     string true  "stream id (uuid)"
//	@Param       before   query    string false "message id: messages older than this one"
//	@Param       limit    query    int    false "page size (1–100)" default(50)
//	@Success     200      {object} response.Data[response.MessagePage]
//	@Failure     400      {object} response.Error "VALIDATION_FAILED"
//	@Failure     404      {object} response.Error "NOT_FOUND"
//	@Router      /chat/rooms/{streamId}/messages [get]
func (r *routes) history(ctx *fiber.Ctx) error {
	streamID := ctx.Params("streamId")
	if _, err := uuid.Parse(streamID); err != nil {
		return errorJSON(ctx, http.StatusNotFound, entity.CodeNotFound, "stream not found", nil)
	}

	var q request.History
	if err := ctx.QueryParser(&q); err != nil {
		return validationFailed(ctx, map[string]string{"limit": "INVALID"})
	}

	if errs := q.Validate(); len(errs) > 0 {
		return validationFailed(ctx, errs)
	}

	var before *string
	if q.Before != "" {
		before = &q.Before
	}

	msgs, next, err := r.chat.History(ctx.UserContext(), streamID, before, q.Limit)
	if err != nil {
		return r.fail(ctx, "history", err)
	}

	out := response.MessagePage{Items: make([]response.Message, 0, len(msgs)), NextCursor: next}
	for _, m := range msgs {
		out.Items = append(out.Items, messageOf(m))
	}

	ctx.Set(fiber.HeaderCacheControl, "no-cache")

	return ctx.JSON(response.Data[response.MessagePage]{Data: out})
}

// emotes godoc
//
//	@Summary     Emotes usable in chat
//	@Description Global emotes; with channelId also the channel's emotes, first, ordered by tierRequired then code. A channel emote is
//	@Description locked for a sender whose snapshot me.emoteTier is below its tierRequired. An unknown or malformed channelId only
//	@Description returns the global emotes.
//	@Tags        chat
//	@Produce     json
//	@Param       channelId query    string false "channel id"
//	@Success     200       {object} response.Data[response.EmoteList]
//	@Router      /chat/emotes [get]
func (r *routes) emotes(ctx *fiber.Ctx) error {
	var channelID *string

	if id, err := uuid.Parse(ctx.Query("channelId")); err == nil {
		s := id.String()
		channelID = &s
	}

	list, err := r.chat.Emotes(ctx.UserContext(), channelID)
	if err != nil {
		return r.fail(ctx, "emotes", err)
	}

	out := response.EmoteList{Items: make([]response.Emote, 0, len(list))}
	for _, e := range list {
		out.Items = append(out.Items, response.Emote{
			ID: e.ID, Code: e.Code, ImageURL: e.ImageURL, Animated: e.Animated, Scope: e.Scope, ChannelID: e.ChannelID,
			TierRequired: e.TierRequired,
		})
	}

	ctx.Set(fiber.HeaderCacheControl, "public, max-age=60")

	return ctx.JSON(response.Data[response.EmoteList]{Data: out})
}

// settings godoc
//
//	@Summary Chat modes of a channel
//	@Tags    chat
//	@Produce json
//	@Param   channelId path     string true "channel id (uuid)"
//	@Success 200       {object} response.Data[response.ChatSettings]
//	@Failure 404       {object} response.Error "NOT_FOUND"
//	@Router  /channels/{channelId}/chat-settings [get]
func (r *routes) settings(ctx *fiber.Ctx) error {
	channelID := ctx.Params("channelId")
	if _, err := uuid.Parse(channelID); err != nil {
		return errorJSON(ctx, http.StatusNotFound, entity.CodeNotFound, "channel not found", nil)
	}

	s, err := r.chat.Settings(ctx.UserContext(), channelID)
	if err != nil {
		return r.fail(ctx, "settings", err)
	}

	ctx.Set(fiber.HeaderCacheControl, "no-cache")

	return ctx.JSON(response.Data[response.ChatSettings]{Data: settingsOf(s)})
}

// updateSettings godoc
//
//	@Summary     Change the chat modes of a channel
//	@Description Channel owner, its moderators and admins. Every open room of the channel gets mode_change and a system message. blockLinks and guestChat absent keep the current value; only the owner and admins may change guestChat (a moderator gets 403).
//	@Tags        chat
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       channelId path     string               true "channel id (uuid)"
//	@Param       body      body     request.UpdateChatSettings true "modes"
//	@Success     200       {object} response.Data[response.ChatSettings]
//	@Failure     400       {object} response.Error "VALIDATION_FAILED (details.slowSec / followersMin = ENUM)"
//	@Failure     401       {object} response.Error "UNAUTHORIZED"
//	@Failure     403       {object} response.Error "FORBIDDEN (not a manager of the channel, or a moderator changing guestChat)"
//	@Router      /channels/{channelId}/chat-settings [put]
func (r *routes) updateSettings(ctx *fiber.Ctx) error {
	userID, ok := middleware.UserID(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusUnauthorized, entity.CodeUnauthorized, "unauthorized", nil)
	}

	channelID := ctx.Params("channelId")
	if _, err := uuid.Parse(channelID); err != nil {
		return errorJSON(ctx, http.StatusNotFound, entity.CodeNotFound, "channel not found", nil)
	}

	var body request.UpdateChatSettings
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, nil)
	}

	if errs := body.Validate(); len(errs) > 0 {
		return validationFailed(ctx, errs)
	}

	p := entity.Principal{UserID: userID, GlobalRole: middleware.Role(ctx), IP: ctx.IP()}

	s, err := r.chat.UpdateSettings(ctx.UserContext(), p, r.settingsFrom(ctx, channelID, body))
	if err != nil {
		return r.fail(ctx, "updateSettings", err)
	}

	return ctx.JSON(response.Data[response.ChatSettings]{Data: settingsOf(s)})
}

// settingsFrom fills the fields the body leaves out (blockLinks, guestChat) with the current values; when they cannot
// be read they are off.
func (r *routes) settingsFrom(ctx *fiber.Ctx, channelID string, body request.UpdateChatSettings) entity.ChatSettings {
	s := entity.ChatSettings{ChannelID: channelID, SlowSec: body.SlowSec, FollowersMin: body.FollowersMin, SubsOnly: body.SubsOnly}

	if body.BlockLinks == nil || body.GuestChat == nil {
		if cur, err := r.chat.Settings(ctx.UserContext(), channelID); err == nil {
			s.BlockLinks, s.GuestChat = cur.BlockLinks, cur.GuestChat
		}
	}

	if body.BlockLinks != nil {
		s.BlockLinks = *body.BlockLinks
	}

	if body.GuestChat != nil {
		s.GuestChat = *body.GuestChat
	}

	return s
}

func settingsOf(s entity.ChatSettings) response.ChatSettings {
	out := response.ChatSettings{
		SlowSec: s.SlowSec, FollowersMin: s.FollowersMin, SubsOnly: s.SubsOnly, BlockLinks: s.BlockLinks, GuestChat: s.GuestChat,
	}

	if s.UpdatedAt != nil {
		t := s.UpdatedAt.UTC()
		out.UpdatedAt = &t
	}

	return out
}

func messageOf(m entity.Message) response.Message {
	frags := make([]response.Fragment, 0, len(m.Fragments))

	for _, f := range m.Fragments {
		rf := response.Fragment{Type: f.Type, Text: f.Text, UserID: f.UserID}
		if f.Emote != nil {
			rf.Emote = &response.EmoteRef{ID: f.Emote.ID, Code: f.Emote.Code, ImageURL: f.Emote.ImageURL}
		}

		frags = append(frags, rf)
	}

	badges := m.User.Badges
	if badges == nil {
		badges = []string{}
	}

	return response.Message{
		ID:       m.ID,
		StreamID: m.StreamID,
		User: response.Author{
			ID: m.User.ID, Username: m.User.Username, DisplayName: m.User.DisplayName, Color: m.User.Color, Badges: badges,
		},
		Body:      m.Body,
		Fragments: frags,
		Type:      m.Type,
		CreatedAt: m.CreatedAt.UTC(),
		Pinned:    m.Pinned,
		Deleted:   m.Deleted,
	}
}
