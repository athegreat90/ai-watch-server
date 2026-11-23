package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

// Options configures [Serve].
type Options struct {
	// Handler serves the requests, normally the result of [NewHandler].
	Handler http.Handler
	// MaxConns is the number of simultaneously open connections; extra clients wait in the
	// kernel backlog. Zero means 16, the limit of the original server.
	MaxConns int
	// ReadTimeout bounds reading a whole request. Zero means 3 seconds.
	ReadTimeout time.Duration
	// WriteTimeout bounds writing a response. Zero means 3 seconds.
	WriteTimeout time.Duration
	// ShutdownTimeout bounds the graceful shutdown. Zero means 5 seconds.
	ShutdownTimeout time.Duration
	// Logger receives lifecycle messages. Nil discards them. Requests are never logged.
	Logger *slog.Logger
}

// orDefault returns v, or def when v is not positive.
func orDefault[T ~int | ~int64](v, def T) T {
	if v <= 0 {
		return def
	}
	return v
}

// capListener limits how many accepted connections are open at once, like the semaphore of
// the Python server. Unlike Python, which accepts and silently closes the excess, the excess
// waits in the kernel backlog until a slot frees, so no client is turned away.
type capListener struct {
	net.Listener
	slots chan struct{}
	done  chan struct{}
	once  sync.Once
}

// newCapListener wraps ln so that at most n connections are open at the same time.
func newCapListener(ln net.Listener, n int) *capListener {
	return &capListener{Listener: ln, slots: make(chan struct{}, n), done: make(chan struct{})}
}

// Accept waits for a free slot, then accepts one connection that releases the slot when closed.
// It returns net.ErrClosed once the listener is closed, even while waiting for a slot.
func (l *capListener) Accept() (net.Conn, error) {
	select {
	case l.slots <- struct{}{}:
	case <-l.done:
		return nil, net.ErrClosed
	}
	c, err := l.Listener.Accept()
	if err != nil {
		<-l.slots
		return nil, err
	}
	return &slotConn{Conn: c, release: func() { <-l.slots }}, nil
}

// Close stops accepting connections and wakes up an Accept that is waiting for a slot.
func (l *capListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return l.Listener.Close()
}

// slotConn is a connection that frees its listener slot exactly once when closed.
type slotConn struct {
	net.Conn
	once    sync.Once
	release func()
}

// Close closes the connection and releases its slot on the first call.
func (c *slotConn) Close() error {
	c.once.Do(c.release)
	return c.Conn.Close()
}

// Serve serves opts.Handler on ln until ctx is cancelled, then shuts down gracefully, letting
// in-flight requests finish for up to opts.ShutdownTimeout. It returns nil after a clean
// shutdown and the underlying error if serving fails. Keep-alive is off, as in the original.
func Serve(ctx context.Context, ln net.Listener, opts Options) error {
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	readTimeout := orDefault(opts.ReadTimeout, 3*time.Second)
	srv := &http.Server{
		Handler:           opts.Handler,
		ReadHeaderTimeout: readTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      orDefault(opts.WriteTimeout, 3*time.Second),
		IdleTimeout:       readTimeout,
	}
	srv.SetKeepAlivesEnabled(false)
	drained := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(drained)
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), orDefault(opts.ShutdownTimeout, 5*time.Second))
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Warn("graceful shutdown incomplete", "error", err)
			_ = srv.Close()
		}
	})
	defer stop()
	logger.Info("listening", "addr", ln.Addr().String())
	err := srv.Serve(newCapListener(ln, orDefault(opts.MaxConns, 16)))
	if errors.Is(err, http.ErrServerClosed) {
		<-drained // Serve returns as soon as shutdown starts; wait until in-flight requests finish.
		return nil
	}
	return err
}
