// Package acpagent drives a coding agent over the Agent Client Protocol:
// JSON-RPC 2.0, one message per line, over the agent subprocess's stdio.
package acpagent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
)

// errExited is what every waiting call gets once the agent's stdout closes.
var errExited = errors.New("acpagent: agent process exited")

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

// handler receives what the agent sends unprompted. onNotify runs on the
// read loop, in arrival order. onRequest runs on its own goroutine, so it
// may block.
type handler interface {
	onNotify(method string, params json.RawMessage)
	onRequest(method string, params json.RawMessage) (any, *rpcError)
}

// conn is one JSON-RPC conversation. Its read loop is the only reader of r.
type conn struct {
	w    io.Writer
	wmu  sync.Mutex
	h    handler
	next atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan message
	err     error

	requests sync.WaitGroup
	done     chan struct{}
}

// newConn starts the read loop. It owns r and stops when r ends, when r
// yields a line that is not JSON-RPC, or when a line exceeds maxBytes.
func newConn(r io.Reader, w io.Writer, maxBytes int, h handler) *conn {
	c := &conn{w: w, h: h, pending: map[int64]chan message{}, done: make(chan struct{})}
	go c.readLoop(r, maxBytes) // owns r, stops when dispatchAll returns
	return c
}

// call sends a request and waits for its response, ctx, or the end of the
// conversation, whichever comes first.
func (c *conn) call(ctx context.Context, method string, params, result any) error {
	p, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("acpagent: encode %s: %w", method, err)
	}
	id := c.next.Add(1)
	ch := make(chan message, 1)

	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return c.err
	}
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.write(message{ID: json.RawMessage(strconv.FormatInt(id, 10)), Method: method, Params: p}); err != nil {
		return err
	}
	select {
	case m := <-ch:
		return decode(method, m, result)
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		select {
		case m := <-ch:
			return decode(method, m, result)
		default:
			return c.failure()
		}
	}
}

// send writes a notification.
func (c *conn) send(method string, params any) error {
	p, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("acpagent: encode %s: %w", method, err)
	}
	return c.write(message{Method: method, Params: p})
}

// wait blocks until the read loop has stopped and every request handler has
// returned.
func (c *conn) wait() {
	<-c.done
	c.requests.Wait()
}

func (c *conn) failure() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// write sends m as one line. json.Marshal never emits a raw newline, so the
// line cannot be split.
func (c *conn) write(m message) error {
	m.JSONRPC = "2.0"
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("acpagent: encode: %w", err)
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := c.w.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("acpagent: write: %w", err)
	}
	return nil
}

func (c *conn) readLoop(r io.Reader, maxBytes int) {
	err := c.dispatchAll(r, maxBytes)
	c.mu.Lock()
	c.err = err
	c.mu.Unlock()
	close(c.done)
}

func (c *conn) dispatchAll(r io.Reader, maxBytes int) error {
	s := bufio.NewScanner(r)
	s.Buffer(nil, maxBytes)
	for s.Scan() {
		var m message
		if err := json.Unmarshal(s.Bytes(), &m); err != nil {
			return fmt.Errorf("acpagent: agent wrote a line that is not JSON-RPC: %q", s.Text())
		}
		c.dispatch(m)
	}
	if errors.Is(s.Err(), bufio.ErrTooLong) {
		return fmt.Errorf("acpagent: agent sent a message over %d bytes", maxBytes)
	}
	if s.Err() != nil {
		return fmt.Errorf("acpagent: read: %w", s.Err())
	}
	return errExited
}

func (c *conn) dispatch(m message) {
	switch {
	case m.Method != "" && len(m.ID) > 0:
		c.requests.Add(1)
		// Owned by the conn; wait joins it. It stops when onRequest returns.
		go func() {
			defer c.requests.Done()
			c.answer(m)
		}()
	case m.Method != "":
		c.h.onNotify(m.Method, m.Params)
	default:
		c.deliver(m)
	}
}

func (c *conn) answer(m message) {
	result, rerr := c.h.onRequest(m.Method, m.Params)
	reply := message{ID: m.ID, Error: rerr}
	if rerr == nil {
		b, err := json.Marshal(result)
		if err != nil {
			reply.Error = &rpcError{Code: -32603, Message: err.Error()}
		} else {
			reply.Result = b
		}
	}
	_ = c.write(reply) // an agent that has gone cannot be answered
}

// deliver hands a response to the call waiting for it. A response nobody is
// waiting for, because its call gave up, is dropped.
func (c *conn) deliver(m message) {
	id, err := strconv.ParseInt(string(m.ID), 10, 64)
	if err != nil {
		return
	}
	c.mu.Lock()
	ch, ok := c.pending[id]
	c.mu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- m:
	default:
	}
}

func decode(method string, m message, result any) error {
	if m.Error != nil {
		return fmt.Errorf("acpagent: %s: %w", method, m.Error)
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(m.Result, result); err != nil {
		return fmt.Errorf("acpagent: decode %s result: %w", method, err)
	}
	return nil
}
