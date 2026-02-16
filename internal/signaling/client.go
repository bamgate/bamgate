package signaling

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/kuuji/bamgate/pkg/protocol"
)

// ClientConfig holds configuration for a signaling Client.
type ClientConfig struct {
	// ServerURL is the WebSocket URL of the signaling server (e.g. "ws://localhost:8080/connect").
	ServerURL string

	// PeerID is this client's unique identifier in the network.
	PeerID string

	// PublicKey is this client's WireGuard public key (base64-encoded).
	PublicKey string

	// Address is this client's WireGuard tunnel address (e.g. "10.0.0.1/24").
	Address string

	// Routes is a list of additional subnets (CIDR notation) reachable through
	// this device, advertised to peers via the join message.
	Routes []string

	// Metadata is an optional map of capability advertisements (routes, DNS,
	// search domains) included in the join message so other peers can
	// discover what this device offers.
	Metadata map[string]string

	// TokenProvider returns the current bearer token for authenticating with
	// the signaling server. Called on each dial attempt so it can return a
	// fresh JWT after token refresh. If nil, no Authorization header is sent.
	TokenProvider func() string

	// Logger is the structured logger to use. If nil, slog.Default() is used.
	Logger *slog.Logger

	// MessageBufferSize is the capacity of the inbound message channel.
	// Defaults to 64 if zero.
	MessageBufferSize int

	// DialTimeout bounds the duration of each WebSocket dial attempt.
	// Defaults to 10s if zero.
	DialTimeout time.Duration

	// Reconnect controls automatic reconnection behavior.
	Reconnect ReconnectConfig
}

// ReconnectConfig controls the reconnection backoff strategy.
type ReconnectConfig struct {
	// Enabled controls whether automatic reconnection is attempted.
	Enabled bool

	// InitialDelay is the delay before the first reconnection attempt.
	// Defaults to 1s.
	InitialDelay time.Duration

	// MaxDelay is the maximum delay between reconnection attempts.
	// Defaults to 30s.
	MaxDelay time.Duration

	// MaxAttempts is the maximum number of reconnection attempts.
	// Zero means unlimited.
	MaxAttempts int
}

// Client is a WebSocket client for the signaling server.
// It connects, sends a join message, and delivers incoming messages
// on a channel. It supports automatic reconnection with exponential backoff.
type Client struct {
	cfg    ClientConfig
	log    *slog.Logger
	msgCh  chan protocol.Message
	done   chan struct{}
	cancel context.CancelFunc

	mu   sync.Mutex
	conn *websocket.Conn
}

// NewClient creates a new signaling client with the given configuration.
// Call Connect to establish the connection and start receiving messages.
func NewClient(cfg ClientConfig) *Client {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	log = log.With("peer_id", cfg.PeerID)

	bufSize := cfg.MessageBufferSize
	if bufSize <= 0 {
		bufSize = 64
	}

	return &Client{
		cfg:   cfg,
		log:   log,
		msgCh: make(chan protocol.Message, bufSize),
		done:  make(chan struct{}),
	}
}

// Messages returns a read-only channel that delivers incoming signaling messages.
// The channel is closed when the client is closed or the context is cancelled
// and reconnection is exhausted.
func (c *Client) Messages() <-chan protocol.Message {
	return c.msgCh
}

// Connect dials the signaling server, sends a join message, and starts
// the receive loop. If reconnection is enabled, it will automatically
// reconnect on connection loss until the context is cancelled or max
// attempts are exhausted.
//
// Connect blocks until the initial connection is established or fails.
// After the initial connection, reconnection happens in the background.
func (c *Client) Connect(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	// Establish the initial connection synchronously so the caller
	// knows immediately if the server is unreachable.
	if err := c.dial(ctx); err != nil {
		cancel()
		return fmt.Errorf("connecting to signaling server: %w", err)
	}

	// Send the join message.
	if err := c.sendJoin(ctx); err != nil {
		cancel()
		c.closeConn()
		return fmt.Errorf("sending join message: %w", err)
	}

	c.log.Info("connected to signaling server", "url", c.cfg.ServerURL)

	// Start the receive loop in a goroutine. It will handle
	// reconnection if configured.
	go c.receiveLoop(ctx)

	return nil
}

// Send sends a signaling message to the server.
func (c *Client) Send(ctx context.Context, msg protocol.Message) error {
	data, err := protocol.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling message: %w", err)
	}

	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()

	if conn == nil {
		return errors.New("not connected")
	}

	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		return fmt.Errorf("writing message: %w", err)
	}

	c.log.Debug("sent message", "type", msg.MessageType())
	return nil
}

// Close gracefully shuts down the client, closing the WebSocket connection
// and the message channel.
func (c *Client) Close() error {
	if c.cancel != nil {
		c.cancel()
	}

	// Wait for the receive loop to finish.
	<-c.done

	return nil
}

// dial establishes a WebSocket connection to the signaling server.
func (c *Client) dial(ctx context.Context) error {
	dialTimeout := c.cfg.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = 10 * time.Second
	}
	dialCtx, dialCancel := context.WithTimeout(ctx, dialTimeout)
	defer dialCancel()

	var opts *websocket.DialOptions
	if c.cfg.TokenProvider != nil {
		if token := c.cfg.TokenProvider(); token != "" {
			opts = &websocket.DialOptions{
				HTTPHeader: http.Header{
					"Authorization": []string{"Bearer " + token},
				},
			}
		}
	}

	conn, _, err := websocket.Dial(dialCtx, c.cfg.ServerURL, opts)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	return nil
}

// sendJoin sends the initial join message on the current connection.
func (c *Client) sendJoin(ctx context.Context) error {
	return c.Send(ctx, &protocol.JoinMessage{
		PeerID:    c.cfg.PeerID,
		PublicKey: c.cfg.PublicKey,
		Address:   c.cfg.Address,
		Routes:    c.cfg.Routes,
		Metadata:  c.cfg.Metadata,
	})
}

// closeConn closes the current WebSocket connection, if any.
func (c *Client) closeConn() {
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()

	if conn != nil {
		conn.Close(websocket.StatusNormalClosure, "closing")
	}
}

// receiveLoop reads messages from the WebSocket and sends them on the message
// channel. If reconnection is enabled, it will reconnect on connection loss.
// It closes the message channel and the done channel when finished.
func (c *Client) receiveLoop(ctx context.Context) {
	defer close(c.done)
	defer close(c.msgCh)

	for {
		err := c.readMessages(ctx)
		if err == nil || ctx.Err() != nil {
			// Clean shutdown or context cancelled.
			c.closeConn()
			return
		}

		c.log.Warn("connection lost", "error", err)
		c.closeConn()

		if !c.cfg.Reconnect.Enabled {
			return
		}

		if !c.reconnect(ctx) {
			return
		}
	}
}

// readMessages reads messages from the current connection until an error
// occurs or the context is cancelled. Returns nil only on clean close.
func (c *Client) readMessages(ctx context.Context) error {
	for {
		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()

		if conn == nil {
			return errors.New("no connection")
		}

		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}

		msg, err := protocol.Unmarshal(data)
		if err != nil {
			c.log.Warn("ignoring malformed message", "error", err)
			continue
		}

		c.log.Debug("received message", "type", msg.MessageType())

		select {
		case c.msgCh <- msg:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// reconnect attempts to re-establish the connection with exponential backoff.
// Returns true if reconnection succeeded, false if it should give up.
func (c *Client) reconnect(ctx context.Context) bool {
	initialDelay := c.cfg.Reconnect.InitialDelay
	if initialDelay <= 0 {
		initialDelay = time.Second
	}
	maxDelay := c.cfg.Reconnect.MaxDelay
	if maxDelay <= 0 {
		maxDelay = 30 * time.Second
	}
	maxAttempts := c.cfg.Reconnect.MaxAttempts

	for attempt := 1; maxAttempts == 0 || attempt <= maxAttempts; attempt++ {
		// Exponential backoff: initialDelay * 2^(attempt-1), capped at maxDelay.
		backoff := time.Duration(float64(initialDelay) * math.Pow(2, float64(attempt-1)))
		if backoff > maxDelay {
			backoff = maxDelay
		}

		c.log.Info("reconnecting", "attempt", attempt, "backoff", backoff)

		select {
		case <-ctx.Done():
			return false
		case <-time.After(backoff):
		}

		if err := c.dial(ctx); err != nil {
			c.log.Warn("reconnection failed", "attempt", attempt, "error", err)
			continue
		}

		if err := c.sendJoin(ctx); err != nil {
			c.log.Warn("rejoin failed", "attempt", attempt, "error", err)
			c.closeConn()
			continue
		}

		c.log.Info("reconnected to signaling server", "attempt", attempt)
		return true
	}

	c.log.Error("reconnection attempts exhausted")
	return false
}
