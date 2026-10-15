package ws

import (
	"encoding/json"
	"time"

	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
)

// Client → server ops (NXL-00 §10.2).
const (
	opAuth        = "auth"
	opSubscribe   = "subscribe"
	opUnsubscribe = "unsubscribe"
	opPing        = "ping"
	opSend        = "send"
)

// Close codes (NXL-00 §10.4).
const (
	CloseServiceRestart = 1012
	CloseUnauthorized   = 4001
	CloseAccountLocked  = 4003
	CloseAuthTimeout    = 4008
	CloseTooMany        = 4029
)

// inFrame is any client frame; fields not used by the op are ignored.
type inFrame struct {
	Op          string          `json:"op"`
	Token       *string         `json:"token"`
	AnonID      string          `json:"anonId"`
	Topic       string          `json:"topic"`
	LastSeq     *int64          `json:"lastSeq"`
	Type        string          `json:"type"`
	Data        json.RawMessage `json:"data"`
	ClientMsgID string          `json:"clientMsgId"`
}

type readyFrame struct {
	Op           string  `json:"op"`
	ConnID       string  `json:"connId"`
	UserID       *string `json:"userId"`
	HeartbeatSec int     `json:"heartbeatSec"`
}

type subscribedFrame struct {
	Op       string `json:"op"`
	Topic    string `json:"topic"`
	Seq      int64  `json:"seq"`
	Snapshot any    `json:"snapshot,omitempty"`
}

// eventFrame: Seq is omitted for connection-local events (ack, sampling), which are not part of the
// topic's sequence and are never replayed.
type eventFrame struct {
	Op    string          `json:"op"`
	Topic string          `json:"topic"`
	Seq   int64           `json:"seq,omitempty"`
	Type  string          `json:"type"`
	Data  json.RawMessage `json:"data"`
	TS    time.Time       `json:"ts"`
}

type topicFrame struct {
	Op    string `json:"op"`
	Topic string `json:"topic"`
}

type errorFrame struct {
	Op      string            `json:"op"`
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Ref     string            `json:"ref,omitempty"`
	Details map[string]string `json:"details,omitempty"`
}

type ackData struct {
	ClientMsgID string `json:"clientMsgId"`
	MessageID   string `json:"messageId"`
}

type samplingData struct {
	Active bool `json:"active"`
}

// encode never fails for these frame types; a failure would be a programming error, so it yields an
// error frame instead of a panic.
func encode(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{"op":"error","code":"INTERNAL_ERROR","message":"internal error"}`)
	}

	return b
}

func encodeEvent(ev entity.Event) []byte {
	return encode(eventFrame{Op: "event", Topic: ev.Topic, Seq: ev.Seq, Type: ev.Type, Data: ev.Data, TS: ev.TS})
}

func encodeLocal(topic, typ string, data any, now time.Time) []byte {
	return encode(eventFrame{Op: "event", Topic: topic, Type: typ, Data: encode(data), TS: now.UTC()})
}

func encodeError(code, message, ref string, details map[string]string) []byte {
	return encode(errorFrame{Op: "error", Code: code, Message: message, Ref: ref, Details: details})
}
