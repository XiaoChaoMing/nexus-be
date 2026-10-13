// Package uploader mirrors one stream's HLS directory into object storage while ffmpeg writes it.
package uploader

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/minhnn/nexuslive-be/services/media/internal/hlsfs"
	"github.com/minhnn/nexuslive-be/services/media/internal/storage"
)

const (
	_pollInterval   = 250 * time.Millisecond
	_parallel       = 4
	_attempts       = 3
	_thumbInterval  = 30 * time.Second
	_flushMaxPasses = 40

	ctPlaylist = "application/vnd.apple.mpegurl"
	ctSegment  = "video/mp2t"
	ctJPEG     = "image/jpeg"

	ccSegment  = "public, max-age=31536000, immutable"
	ccLive     = "no-cache, max-age=1"
	ccClosed   = "public, max-age=3600"
	ccThumb    = "public, max-age=30"
	masterName = "master.m3u8"
	indexName  = "index.m3u8"
	thumbName  = "thumb.jpg"
)

var _backoff = []time.Duration{200 * time.Millisecond, 800 * time.Millisecond, 2 * time.Second} //nolint:gochecknoglobals // retry schedule

// ErrMissingSegments -.
var ErrMissingSegments = errors.New("uploader - Flush: playlists still reference missing segments")

// Uploader -.
type Uploader struct {
	store  storage.ObjectStore
	dir    string
	prefix string // streams/{streamId}/

	mu        sync.Mutex
	uploaded  map[string]bool     // segment rel path → uploaded
	published map[string][32]byte // playlist rel path → hash of last uploaded content
	thumbAt   time.Time
	thumbMod  time.Time

	errors atomic.Int64
}

// New -.
func New(store storage.ObjectStore, dir, streamID string) *Uploader {
	return &Uploader{
		store:     store,
		dir:       dir,
		prefix:    path.Join(strings.TrimSuffix(storage.StreamsPrefix, "/"), streamID) + "/",
		uploaded:  map[string]bool{},
		published: map[string][32]byte{},
	}
}

// Errors is the number of failed uploads so far (after retries).
func (u *Uploader) Errors() int64 {
	return u.errors.Load()
}

// Run syncs until ctx is canceled.
func (u *Uploader) Run(ctx context.Context) {
	t := time.NewTicker(_pollInterval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = u.Sync(ctx, false) //nolint:errcheck // counted in Errors, retried next tick
		}
	}
}

// Flush syncs until everything on disk is uploaded; playlists get the long cache of a closed stream.
func (u *Uploader) Flush(ctx context.Context) error {
	var err error

	for range _flushMaxPasses {
		var pending bool

		pending, err = u.sync(ctx, true)
		if err == nil && !pending {
			return nil
		}

		if ctx.Err() != nil {
			return fmt.Errorf("uploader - Flush: %w (last: %w)", ctx.Err(), err)
		}

		time.Sleep(_pollInterval)
	}

	if err == nil {
		err = ErrMissingSegments
	}

	return err
}

// Sync performs one pass. Segments first; a playlist only after every segment it lists is uploaded.
func (u *Uploader) Sync(ctx context.Context, final bool) error {
	_, err := u.sync(ctx, final)

	return err
}

func (u *Uploader) sync(ctx context.Context, final bool) (bool, error) {
	segments, playlists, err := u.scan()
	if err != nil {
		return true, err
	}

	errs := u.uploadSegments(ctx, segments)

	pending := false

	for _, rel := range playlists {
		ok, perr := u.uploadPlaylist(ctx, rel, final)
		if perr != nil {
			errs = append(errs, perr)
		}

		if !ok {
			pending = true
		}
	}

	if terr := u.uploadThumb(ctx, final); terr != nil {
		errs = append(errs, terr)
	}

	return pending || len(errs) > 0, errors.Join(errs...)
}

func (u *Uploader) scan() (segments, playlists []string, err error) {
	err = filepath.WalkDir(u.dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		rel, relErr := filepath.Rel(u.dir, p)
		if relErr != nil {
			return relErr
		}

		rel = filepath.ToSlash(rel)

		switch {
		case strings.HasSuffix(rel, ".ts"):
			segments = append(segments, rel)
		case strings.HasSuffix(rel, ".m3u8"):
			playlists = append(playlists, rel)
		}

		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}

	if err != nil {
		return nil, nil, fmt.Errorf("uploader - scan: %w", err)
	}

	return segments, playlists, nil
}

func (u *Uploader) uploadSegments(ctx context.Context, segments []string) []error {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
		sem  = make(chan struct{}, _parallel)
	)

	for _, rel := range segments {
		u.mu.Lock()
		done := u.uploaded[rel]
		u.mu.Unlock()

		if done {
			continue
		}

		wg.Add(1)

		sem <- struct{}{}

		go func(rel string) {
			defer wg.Done()
			defer func() { <-sem }()

			if err := u.putFile(ctx, rel, ctSegment, ccSegment); err != nil {
				mu.Lock()

				errs = append(errs, err)
				mu.Unlock()

				return
			}

			u.mu.Lock()
			u.uploaded[rel] = true
			u.mu.Unlock()
		}(rel)
	}

	wg.Wait()

	return errs
}

// uploadPlaylist returns false while the playlist cannot be published yet.
func (u *Uploader) uploadPlaylist(ctx context.Context, rel string, final bool) (bool, error) {
	data, err := os.ReadFile(filepath.Join(u.dir, filepath.FromSlash(rel)))
	if err != nil {
		return false, fmt.Errorf("uploader - read %s: %w", rel, err)
	}

	// Between a disconnect and the reconnect/grace timeout ffmpeg has already closed the playlist;
	// viewers must keep seeing a live (EVENT) playlist until the stream is really over.
	if !final {
		data = hlsfs.WithoutEndList(data)
	}

	cc := ccLive
	if final {
		cc = ccClosed
	}

	// Re-upload when either content or caching changes (the final pass flips every playlist to the long cache).
	sum := sha256.Sum256(append([]byte(cc+"\n"), data...))

	u.mu.Lock()
	same := u.published[rel] == sum
	u.mu.Unlock()

	if same {
		return true, nil
	}

	if path.Base(rel) == indexName {
		base := path.Dir(rel)

		u.mu.Lock()
		for _, s := range hlsfs.Segments(data) {
			if !u.uploaded[path.Join(base, s)] {
				u.mu.Unlock()

				return false, nil
			}
		}
		u.mu.Unlock()
	}

	if err := u.put(ctx, rel, data, ctPlaylist, cc); err != nil {
		return false, err
	}

	u.mu.Lock()
	u.published[rel] = sum
	u.mu.Unlock()

	return true, nil
}

func (u *Uploader) uploadThumb(ctx context.Context, final bool) error {
	info, err := os.Stat(filepath.Join(u.dir, thumbName))
	if err != nil {
		return nil //nolint:nilerr // no thumbnail yet
	}

	u.mu.Lock()
	due := info.ModTime().After(u.thumbMod) && (final || time.Since(u.thumbAt) >= _thumbInterval)
	u.mu.Unlock()

	if !due {
		return nil
	}

	if err := u.putFile(ctx, thumbName, ctJPEG, ccThumb); err != nil {
		return err
	}

	u.mu.Lock()
	u.thumbAt = time.Now()
	u.thumbMod = info.ModTime()
	u.mu.Unlock()

	return nil
}

func (u *Uploader) putFile(ctx context.Context, rel, contentType, cacheControl string) error {
	data, err := os.ReadFile(filepath.Join(u.dir, filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("uploader - read %s: %w", rel, err)
	}

	return u.put(ctx, rel, data, contentType, cacheControl)
}

func (u *Uploader) put(ctx context.Context, rel string, data []byte, contentType, cacheControl string) error {
	var err error

	for attempt := range _attempts {
		if err = u.store.Put(ctx, u.prefix+rel, data, contentType, cacheControl); err == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("uploader - put %s: %w", rel, ctx.Err())
		case <-time.After(_backoff[attempt]):
		}
	}

	u.errors.Add(1)

	return fmt.Errorf("uploader - put %s after %d attempts: %w", rel, _attempts, err)
}
