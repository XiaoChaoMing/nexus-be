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
	chat usecase.Chat
	l    logger.Interface
}

// NewRoutes registers /v1/chat/* and /v1/channels/{channelId}/chat-settings.
func NewRoutes(group fiber.Router, chat usecase.Chat, auth fiber.Handler, l logger.Interface) {
	r := &routes{chat: chat, l: l}

	group.Get("/chat/rooms/:streamId/messages", r.history)
	group.Get("/chat/emotes", r.emotes)
	group.Get("/channels/:channelId/chat-settings", r.settings)
	group.Put("/channels/:channelId/chat-settings", auth, r.updateSettings)
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
//	@Description Global emotes; channelId is accepted and ignored until channel emotes exist.
//	@Tags        chat
//	@Produce     json
//	@Param       channelId query    string false "channel id (ignored for now)"
//	@Success     200       {object} response.Data[response.EmoteList]
//	@Router      /chat/emotes [get]
func (r *routes) emotes(ctx *fiber.Ctx) error {
	list, err := r.chat.Emotes(ctx.UserContext())
	if err != nil {
		return r.fail(ctx, "emotes", err)
	}

	out := response.EmoteList{Items: make([]response.Emote, 0, len(list))}
	for _, e := range list {
		out.Items = append(out.Items, response.Emote{ID: e.ID, Code: e.Code, ImageURL: e.ImageURL, Animated: e.Animated, Scope: e.Scope})
	}

	ctx.Set(fiber.HeaderCacheControl, "public, max-age=300")

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
//	@Description Channel owner, its moderators and admins. Every open room of the channel gets mode_change and a system message.
//	@Tags        chat
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       channelId path     string               true "channel id (uuid)"
//	@Param       body      body     request.UpdateChatSettings true "modes"
//	@Success     200       {object} response.Data[response.ChatSettings]
//	@Failure     400       {object} response.Error "VALIDATION_FAILED (details.slowSec / followersMin = ENUM)"
//	@Failure     401       {object} response.Error "UNAUTHORIZED"
//	@Failure     403       {object} response.Error "FORBIDDEN"
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

	s, err := r.chat.UpdateSettings(ctx.UserContext(), p, entity.ChatSettings{
		ChannelID: channelID, SlowSec: body.SlowSec, FollowersMin: body.FollowersMin, SubsOnly: body.SubsOnly,
	})
	if err != nil {
		return r.fail(ctx, "updateSettings", err)
	}

	return ctx.JSON(response.Data[response.ChatSettings]{Data: settingsOf(s)})
}

func settingsOf(s entity.ChatSettings) response.ChatSettings {
	out := response.ChatSettings{SlowSec: s.SlowSec, FollowersMin: s.FollowersMin, SubsOnly: s.SubsOnly}

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
	}
}
