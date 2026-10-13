package storage

import (
	"context"
	"errors"
	"sync"
)

// Object is a stored object in Memory.
type Object struct {
	Body         []byte
	ContentType  string
	CacheControl string
}

// Memory is an in-process ObjectStore for tests.
type Memory struct {
	mu      sync.Mutex
	objects map[string]Object
	order   []string
	// FailNext makes the next n Put calls fail.
	FailNext int
}

// NewMemory -.
func NewMemory() *Memory {
	return &Memory{objects: map[string]Object{}}
}

// ErrInjected -.
var ErrInjected = errors.New("storage: injected failure")

// Put -.
func (m *Memory) Put(_ context.Context, key string, body []byte, contentType, cacheControl string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.FailNext > 0 {
		m.FailNext--

		return ErrInjected
	}

	m.objects[key] = Object{Body: append([]byte(nil), body...), ContentType: contentType, CacheControl: cacheControl}
	m.order = append(m.order, key)

	return nil
}

// Get -.
func (m *Memory) Get(key string) (Object, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	o, ok := m.objects[key]

	return o, ok
}

// Order returns keys in the order of successful puts (repeats included).
func (m *Memory) Order() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]string(nil), m.order...)
}
