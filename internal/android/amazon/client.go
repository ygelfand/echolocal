// Package amazon owns the small Android-media bridge used on Pryon-capable installations.
// The helper supplies 16 kHz mono PCM, accepts 48 kHz stereo PCM, and forwards wake metadata.
package amazon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/lib/hook"
	"github.com/ygelfand/echolocal/internal/service"
)

const socketName = "echolocal-amazon"

type Client struct {
	mu   sync.Mutex
	conn *net.UnixConn
	proc process

	writeMu sync.Mutex
	audio   hook.Hook[[]byte]
	wake    hook.Hook[Wake]
}

var (
	once   sync.Once
	shared *Client
)

func init() {
	if Enabled() {
		component.Register(component.Hardware, Get(), component.Order(1),
			component.Supervise(service.Required(), service.Restart(time.Second, 30*time.Second)))
	}
}

// Enabled reports whether this installation chose Android media rather than direct ALSA.
func Enabled() bool {
	st, err := os.Stat(layout.AndroidMediaJar)
	return err == nil && st.Mode().IsRegular()
}

func Get() *Client {
	once.Do(func() { shared = &Client{} })
	return shared
}

func (c *Client) Name() string { return "amazon media" }

func (c *Client) ListenAudio(fn func([]byte)) func() { return c.audio.Listen(fn) }
func (c *Client) ListenWake(fn func(Wake)) func()    { return c.wake.Listen(fn) }

func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

// Start launches the user-owned helper and establishes its abstract local socket before the
// microphone or speaker services start.
func (c *Client) Start(ctx context.Context) error {
	_ = c.Close()

	conn, err := dial()
	if err != nil {
		proc, startErr := startProcess()
		if startErr != nil {
			return startErr
		}
		c.mu.Lock()
		c.proc = proc
		c.mu.Unlock()

		conn, err = waitForSocket(ctx, 10*time.Second)
		if err != nil {
			_ = c.Close()
			return err
		}
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	if err := c.send(msgStartCapture, nil); err != nil {
		_ = c.Close()
		return err
	}
	slog.Info("amazon media connected", "socket", "@"+socketName)
	return nil
}

func dial() (*net.UnixConn, error) {
	return net.DialUnix("unix", nil, &net.UnixAddr{Name: "\x00" + socketName, Net: "unix"})
}

func waitForSocket(ctx context.Context, timeout time.Duration) (*net.UnixConn, error) {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		conn, err := dial()
		if err == nil {
			return conn, nil
		}
		last = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("amazon: helper socket did not appear within %s: %w", timeout, last)
}

// Run owns the socket reader. Closing the connection on cancellation is what unblocks ReadFull.
func (c *Client) Run(ctx context.Context) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return errors.New("amazon: helper is not connected")
	}

	closed := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-closed:
		}
	}()
	defer close(closed)

	for {
		kind, payload, err := readFrame(conn)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		switch kind {
		case msgAudio:
			c.audio.Emit(payload)
		case msgWake:
			wake, err := decodeWake(payload)
			if err != nil {
				slog.Warn("amazon wake event rejected", "err", err)
				continue
			}
			c.wake.Emit(wake)
		default:
			slog.Warn("amazon helper sent unknown message", "type", kind, "bytes", len(payload))
		}
	}
}

func (c *Client) Play(pcm []byte) error { return c.send(msgPlay, pcm) }
func (c *Client) StopPlayback() error   { return c.send(msgPlayStop, nil) }

func (c *Client) send(kind byte, payload []byte) error {
	encoded, err := frame(kind, payload)
	if err != nil {
		return err
	}
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return errors.New("amazon: helper is not connected")
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = conn.Write(encoded)
	return err
}

func (c *Client) Close() error {
	c.mu.Lock()
	conn, proc := c.conn, c.proc
	c.conn, c.proc = nil, nil
	c.mu.Unlock()

	var errs []error
	if conn != nil {
		// Best effort: either message may fail because the reader already observed the disconnect.
		c.writeMu.Lock()
		if p, err := frame(msgStopCapture, nil); err == nil {
			_, _ = conn.Write(p)
		}
		if p, err := frame(msgPlayStop, nil); err == nil {
			_, _ = conn.Write(p)
		}
		c.writeMu.Unlock()
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, err)
		}
	}
	if proc != nil {
		if err := proc.Stop(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
