// Package rtmpserver accepts OBS publishes over RTMP and feeds the session pipeline.
package rtmpserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	ev "github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/media/internal/authz"
	"github.com/minhnn/nexuslive-be/services/media/internal/entity"
	"github.com/minhnn/nexuslive-be/services/media/internal/pipeline"
	"github.com/minhnn/nexuslive-be/services/media/internal/session"
	flvtag "github.com/yutopp/go-flv/tag"
	"github.com/yutopp/go-rtmp"
	rtmpmsg "github.com/yutopp/go-rtmp/message"
)

const (
	_authorizeTimeout = 2 * time.Second
	_readIdleTimeout  = 15 * time.Second
	_defaultHeight    = 1080
	// Without onMetaData, wait this long (stream time) after the first video tag for an audio tag.
	_audioProbe = 3 * time.Second
	// Hard cap on tags held before ffmpeg starts.
	_maxPending = 2000
	// First byte of an FLV video tag: frame type (high nibble) | codec id (low nibble);
	// of an audio tag: sound format in the high nibble.
	_flvCodecMask  = 0x0f
	_flvSoundShift = 4
)

type pendingTag struct {
	typ  byte
	ts   uint32
	body []byte
}

// handler serves one RTMP connection. go-rtmp calls it from a single goroutine.
type handler struct {
	rtmp.DefaultHandler

	l     logger.Interface
	auth  authz.Authorizer
	mgr   *session.Manager
	conn  net.Conn
	appOK bool

	pub     *session.Publication
	started bool

	meta       *entity.Source
	metaTag    []byte
	pending    []pendingTag
	firstVideo *uint32
	sawAudio   bool
}

var _ rtmp.Handler = (*handler)(nil)

var (
	errUnknownApp        = errors.New("rtmpserver - unknown app")
	errAlreadyPublishing = errors.New("rtmpserver - already publishing")
	errNoVideo           = errors.New("no video within the probe window")
)

func (h *handler) remoteIP() string {
	host, _, err := net.SplitHostPort(h.conn.RemoteAddr().String())
	if err != nil {
		return h.conn.RemoteAddr().String()
	}

	return host
}

// OnConnect -.
func (h *handler) OnConnect(_ uint32, cmd *rtmpmsg.NetConnectionConnect) error {
	h.appOK = strings.Trim(cmd.Command.App, "/") == "live"
	if !h.appOK {
		return fmt.Errorf("%w %q", errUnknownApp, cmd.Command.App)
	}

	return nil
}

// OnPublish authorizes the stream key and reserves a session.
func (h *handler) OnPublish(_ *rtmp.StreamContext, _ uint32, cmd *rtmpmsg.NetStreamPublish) error {
	if h.pub != nil {
		return errAlreadyPublishing
	}

	key, _, _ := strings.Cut(cmd.PublishingName, "?")

	if !h.mgr.HasCapacity() {
		h.l.Warn("rtmp: publish refused from %s: %v", h.remoteIP(), entity.ErrCapacity)
		h.mgr.Rejected(session.RejectCapacity)

		return entity.ErrCapacity
	}

	ctx, cancel := context.WithTimeout(context.Background(), _authorizeTimeout)
	defer cancel()

	grant, err := h.auth.Authorize(ctx, key, h.remoteIP())
	if err != nil {
		// Never log the key itself.
		h.l.Warn("rtmp: publish rejected from %s: %v", h.remoteIP(), err)
		h.mgr.Rejected(rejectReason(err))

		return err
	}

	pub, err := h.mgr.Begin(grant, func() { _ = h.conn.Close() }) // kicks the publisher
	if err != nil {
		h.l.Warn("rtmp: publish refused for channel %s from %s: %v", grant.ChannelID, h.remoteIP(), err)
		h.mgr.Rejected(rejectReason(err))

		return err
	}

	h.pub = pub
	_ = h.conn.SetDeadline(time.Now().Add(_readIdleTimeout)) //nolint:errcheck // best effort

	h.l.Info("rtmp: publish accepted, stream %s from %s", pub.StreamID(), h.remoteIP())

	return nil
}

// OnSetDataFrame reads onMetaData (size, audio) and keeps the raw tag for ffmpeg.
func (h *handler) OnSetDataFrame(_ uint32, data *rtmpmsg.NetStreamSetDataFrame) error {
	if h.pub == nil || h.started {
		return nil
	}

	var script flvtag.ScriptData
	if err := flvtag.DecodeScriptData(bytes.NewReader(data.Payload), &script); err != nil {
		h.l.Warn("rtmp: stream %s: bad onMetaData ignored: %v", h.pub.StreamID(), err)

		return nil
	}

	md, ok := script.Objects["onMetaData"]
	if !ok {
		return nil
	}

	src, err := sourceFromMeta(md)
	if err != nil {
		return h.reject(err)
	}

	h.meta = &src

	h.metaTag = append([]byte(nil), data.Payload...)

	return nil
}

// sourceFromMeta reads size and audio presence from onMetaData and refuses codecs other than H.264/AAC.
func sourceFromMeta(md map[string]any) (entity.Source, error) {
	src := entity.Source{Height: _defaultHeight}
	if v, ok := md["height"].(float64); ok && v > 0 {
		src.Height = int(v)
	}

	if v, ok := md["width"].(float64); ok && v > 0 {
		src.Width = int(v)
	}

	_, src.HasAudio = md["audiocodecid"]

	if v, ok := md["videocodecid"].(float64); ok && int(v) != int(flvtag.CodecIDAVC) {
		return entity.Source{}, entity.ErrUnsupportedCodec
	}

	if v, ok := md["audiocodecid"].(float64); ok && int(v) != int(flvtag.SoundFormatAAC) {
		return entity.Source{}, entity.ErrUnsupportedCodec
	}

	return src, nil
}

// OnAudio -.
func (h *handler) OnAudio(timestamp uint32, payload io.Reader) error {
	return h.onMedia(pipeline.TagAudio, timestamp, payload)
}

// OnVideo -.
func (h *handler) OnVideo(timestamp uint32, payload io.Reader) error {
	return h.onMedia(pipeline.TagVideo, timestamp, payload)
}

func (h *handler) onMedia(typ byte, ts uint32, payload io.Reader) error {
	if h.pub == nil {
		return nil
	}

	_ = h.conn.SetDeadline(time.Now().Add(_readIdleTimeout)) //nolint:errcheck // best effort

	// The payload buffer is recycled by go-rtmp: copy it.
	body, err := io.ReadAll(payload)
	if err != nil {
		return err
	}

	if len(body) == 0 {
		return nil
	}

	if h.started {
		return h.pub.WriteTag(typ, ts, body)
	}

	if err = checkCodec(typ, body[0]); err != nil {
		return h.reject(err)
	}

	if typ == pipeline.TagAudio {
		h.sawAudio = true
	} else if h.firstVideo == nil {
		h.firstVideo = &ts
	}

	h.pending = append(h.pending, pendingTag{typ: typ, ts: ts, body: body})

	src, ready := h.readySource(ts)
	if !ready {
		if len(h.pending) > _maxPending {
			return h.reject(errNoVideo)
		}

		return nil
	}

	return h.start(src)
}

// readySource decides when enough is known to spawn ffmpeg.
func (h *handler) readySource(now uint32) (entity.Source, bool) {
	if h.firstVideo == nil {
		return entity.Source{}, false
	}

	if h.meta != nil {
		src := *h.meta
		// Metadata may omit audio while audio tags flow (or the reverse); trust what we see when we can.
		src.HasAudio = src.HasAudio || h.sawAudio

		return src, true
	}

	if h.sawAudio || time.Duration(now-*h.firstVideo)*time.Millisecond >= _audioProbe {
		return entity.Source{Height: _defaultHeight, HasAudio: h.sawAudio}, true
	}

	return entity.Source{}, false
}

func (h *handler) start(src entity.Source) error {
	if err := h.pub.Start(src); err != nil {
		h.l.Error("rtmp: stream %s: start transcoder: %v", h.pub.StreamID(), err)

		return err
	}

	h.started = true

	if h.metaTag != nil {
		if err := h.pub.WriteTag(pipeline.TagScript, 0, h.metaTag); err != nil {
			return err
		}
	}

	for _, t := range h.pending {
		if t.typ == pipeline.TagAudio && !src.HasAudio {
			continue
		}

		if err := h.pub.WriteTag(t.typ, t.ts, t.body); err != nil {
			return err
		}
	}

	h.pending = nil

	return nil
}

func (h *handler) reject(err error) error {
	h.l.Warn("rtmp: stream %s from %s rejected: %v", h.pub.StreamID(), h.remoteIP(), err)

	return err
}

// OnClose -.
func (h *handler) OnClose() {
	if h.pub == nil {
		return
	}

	h.pub.Close(ev.ReasonPublisherDisconnected)
	h.pub = nil
}

func checkCodec(typ, first byte) error {
	switch typ {
	case pipeline.TagVideo:
		if flvtag.CodecID(first&_flvCodecMask) != flvtag.CodecIDAVC {
			return entity.ErrUnsupportedCodec
		}
	case pipeline.TagAudio:
		if flvtag.SoundFormat(first>>_flvSoundShift) != flvtag.SoundFormatAAC {
			return entity.ErrUnsupportedCodec
		}
	}

	return nil
}

func rejectReason(err error) string {
	switch {
	case errors.Is(err, entity.ErrCapacity):
		return session.RejectCapacity
	case errors.Is(err, entity.ErrUnauthorized):
		return session.RejectInvalidKey
	case errors.Is(err, entity.ErrAlreadyLive):
		return session.RejectAlreadyLive
	case errors.Is(err, entity.ErrRecentlyEnded):
		return session.RejectRecentlyEnded
	case errors.Is(err, entity.ErrSuspended):
		return session.RejectSuspended
	default:
		return session.RejectError
	}
}
