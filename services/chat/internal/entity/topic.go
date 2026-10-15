package entity

import (
	"strings"

	"github.com/google/uuid"
)

// TopicKind is the prefix of a realtime topic (NXL-00 §10.3).
type TopicKind string

// Topic kinds served by the gateway.
const (
	TopicChat   TopicKind = "chat"
	TopicStream TopicKind = "stream"
	TopicUser   TopicKind = "user"
	// TopicSidebar carries the live state of the channels a user follows (sidebar:{userId}).
	TopicSidebar TopicKind = "sidebar"
)

// Topic is "<kind>:<uuid>".
type Topic struct {
	Kind TopicKind
	ID   string
}

// ChatTopic -.
func ChatTopic(streamID string) Topic { return Topic{Kind: TopicChat, ID: streamID} }

// StreamTopic -.
func StreamTopic(streamID string) Topic { return Topic{Kind: TopicStream, ID: streamID} }

// UserTopic -.
func UserTopic(userID string) Topic { return Topic{Kind: TopicUser, ID: userID} }

// SidebarTopic -.
func SidebarTopic(userID string) Topic { return Topic{Kind: TopicSidebar, ID: userID} }

// ParseTopic accepts chat:{uuid}, stream:{uuid}, user:{uuid} and sidebar:{uuid}; the id is lower-cased.
func ParseTopic(s string) (Topic, error) {
	kind, id, ok := strings.Cut(s, ":")
	if !ok {
		return Topic{}, ErrInvalidTopic
	}

	switch TopicKind(kind) {
	case TopicChat, TopicStream, TopicUser, TopicSidebar:
	default:
		return Topic{}, ErrInvalidTopic
	}

	u, err := uuid.Parse(id)
	if err != nil || len(id) != len(u.String()) {
		return Topic{}, ErrInvalidTopic
	}

	return Topic{Kind: TopicKind(kind), ID: u.String()}, nil
}

func (t Topic) String() string { return string(t.Kind) + ":" + t.ID }

// StreamID returns the stream of a chat: or stream: topic ("" for user: and sidebar:).
func (t Topic) StreamID() string {
	if t.Kind == TopicChat || t.Kind == TopicStream {
		return t.ID
	}

	return ""
}
