package viewers_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase/viewers"
	"github.com/stretchr/testify/require"
)

// fakePresence is the Redis viewer state shared by several instances.
type fakePresence struct {
	repo.PresenceStore

	mu     sync.Mutex
	zsets  map[string]map[string]time.Time
	counts map[string]int
	peaks  map[string]int
	leader string
}

func newPresence() *fakePresence {
	return &fakePresence{zsets: map[string]map[string]time.Time{}, counts: map[string]int{}, peaks: map[string]int{}}
}

func (f *fakePresence) TouchViewers(_ context.Context, streamID string, keys []string, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	z, ok := f.zsets[streamID]
	if !ok {
		z = map[string]time.Time{}
		f.zsets[streamID] = z
	}

	for _, k := range keys {
		z[k] = now
	}

	return nil
}

func (f *fakePresence) CountViewers(_ context.Context, streamID string, since time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	n := 0

	for _, at := range f.zsets[streamID] {
		if !at.Before(since) {
			n++
		}
	}

	return n, nil
}

func (f *fakePresence) LeadViewers(_ context.Context, instance string, _ time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.leader == "" {
		f.leader = instance
	}

	return f.leader == instance, nil
}

func (f *fakePresence) WatchedStreams(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := []string{}
	for id := range f.zsets {
		out = append(out, id)
	}

	return out, nil
}

func (f *fakePresence) RefreshCount(_ context.Context, streamID string, cutoff time.Time, _ time.Duration) (count int, changed bool, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for k, at := range f.zsets[streamID] {
		if at.Before(cutoff) {
			delete(f.zsets[streamID], k)
		}
	}

	n := len(f.zsets[streamID])
	old, had := f.counts[streamID]
	f.counts[streamID] = n
	f.peaks[streamID] = max(f.peaks[streamID], n)

	return n, !had || old != n, nil
}

func (f *fakePresence) ForgetStream(_ context.Context, streamID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	delete(f.zsets, streamID)

	return nil
}

type recPub struct {
	mu     sync.Mutex
	counts []int
}

func (p *recPub) Publish(_ context.Context, topic entity.Topic, typ string, data any) (entity.Event, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if typ == entity.EventViewerCount && topic.Kind == entity.TopicStream {
		var c struct {
			Count int `json:"count"`
		}

		b, err := json.Marshal(data)
		if err != nil {
			return entity.Event{}, err
		}

		if uerr := json.Unmarshal(b, &c); uerr != nil {
			return entity.Event{}, uerr
		}

		p.counts = append(p.counts, c.Count)
	}

	return entity.Event{}, nil
}

const stream = "0199d4a2-0000-7000-8000-0000000000aa"

func TestOnePersonCountsOnceAcrossTabsAndInstances(t *testing.T) { //nolint:funlen // one story
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	store, pub := newPresence(), &recPub{}

	a := viewers.New(store, pub, "a", 10*time.Second, logger.New("error")).WithClock(clock)
	b := viewers.New(store, pub, "b", 10*time.Second, logger.New("error")).WithClock(clock)

	alice := entity.Principal{UserID: "u1", ConnID: "c1"}.ViewerKey()
	aliceTab2 := entity.Principal{UserID: "u1", ConnID: "c2"}.ViewerKey()
	guest := entity.Principal{AnonID: "anon-1", ConnID: "c3"}.ViewerKey()
	guestTab2 := entity.Principal{AnonID: "anon-1", ConnID: "c4"}.ViewerKey()
	noAnon := entity.Principal{ConnID: "c5"}.ViewerKey()

	// Alice: chat: and stream: topics in one tab on instance a, a second tab on instance b.
	n, err := a.Join(ctx, stream, alice)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	_, err = a.Join(ctx, stream, alice)
	require.NoError(t, err)

	n, err = b.Join(ctx, stream, aliceTab2)
	require.NoError(t, err)
	require.Equal(t, 1, n, "same account on another instance is the same viewer")

	// A guest with two tabs (same anonId), and a guest without anonId.
	_, err = a.Join(ctx, stream, guest)
	require.NoError(t, err)

	n, err = b.Join(ctx, stream, guestTab2)
	require.NoError(t, err)
	require.Equal(t, 2, n)

	n, err = b.Join(ctx, stream, noAnon)
	require.NoError(t, err)
	require.Equal(t, 3, n)

	require.NoError(t, a.Tick(ctx))
	require.NoError(t, b.Tick(ctx))
	require.Equal(t, []int{3}, pub.counts, "only the lease holder publishes, and only changes")

	// Every connection on instance b goes away: alice and the guest still watch on a.
	b.Leave(stream, aliceTab2)
	b.Leave(stream, guestTab2)
	b.Leave(stream, noAnon)

	now = now.Add(30 * time.Second)

	require.NoError(t, b.Tick(ctx))
	require.NoError(t, a.Tick(ctx))
	require.Equal(t, []int{3, 2}, pub.counts)
	require.Equal(t, 3, store.peaks[stream])

	// Alice leaves her first tab only partly (two subscriptions): still counted.
	a.Leave(stream, alice)

	now = now.Add(30 * time.Second)

	require.NoError(t, a.Tick(ctx))
	require.Equal(t, []int{3, 2}, pub.counts)

	a.Leave(stream, alice)
	a.Leave(stream, guest)

	now = now.Add(30 * time.Second)

	require.NoError(t, a.Tick(ctx))
	require.Equal(t, []int{3, 2, 0}, pub.counts)

	_, watched := store.zsets[stream]
	require.False(t, watched, "a stream without viewers is forgotten")
}
