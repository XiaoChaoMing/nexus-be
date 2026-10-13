package rtmpserver

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/media/internal/authz"
	"github.com/minhnn/nexuslive-be/services/media/internal/session"
	"github.com/sirupsen/logrus"
	"github.com/yutopp/go-rtmp"
)

const (
	_handshakeTimeout = 10 * time.Second
	_maxMessageSize   = 8 * 1024 * 1024
	_bandwidthWindow  = 6 * 1024 * 1024 / 8
)

// Server -.
type Server struct {
	l    logger.Interface
	srv  *rtmp.Server
	ln   net.Listener
	errC chan error
}

// New listens on addr; Start begins accepting.
func New(addr string, l logger.Interface, auth authz.Authorizer, mgr *session.Manager) (*Server, error) {
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", addr)
	if err != nil {
		return nil, err
	}

	lr := logrus.New()
	lr.SetLevel(logrus.WarnLevel)

	s := &Server{l: l, ln: ln, errC: make(chan error, 1)}
	s.srv = rtmp.NewServer(&rtmp.ServerConfig{
		OnConnect: func(conn net.Conn) (io.ReadWriteCloser, *rtmp.ConnConfig) {
			_ = conn.SetDeadline(time.Now().Add(_handshakeTimeout)) //nolint:errcheck // cleared on publish

			return conn, &rtmp.ConnConfig{
				Handler: &handler{l: l, auth: auth, mgr: mgr, conn: conn},
				ControlState: rtmp.StreamControlStateConfig{
					DefaultBandwidthWindowSize: _bandwidthWindow,
					MaxMessageSize:             _maxMessageSize,
				},
				Logger: lr,
			}
		},
	})

	return s, nil
}

// Addr -.
func (s *Server) Addr() net.Addr {
	return s.ln.Addr()
}

// Start -.
func (s *Server) Start() {
	go func() {
		err := s.srv.Serve(s.ln)
		if err != nil && !errors.Is(err, rtmp.ErrClosed) {
			s.errC <- err
		}

		close(s.errC)
	}()
}

// Notify -.
func (s *Server) Notify() <-chan error {
	return s.errC
}

// Close stops accepting; existing publishes are kicked by the session manager.
func (s *Server) Close() error {
	return s.srv.Close()
}
