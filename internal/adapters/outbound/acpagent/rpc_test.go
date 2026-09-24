package acpagent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorder is a handler that records notifications and answers requests.
type recorder struct {
	mu     sync.Mutex
	notes  []string
	answer func(method string) (any, *rpcError)
}

func (r *recorder) onNotify(method string, params json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notes = append(r.notes, method+" "+string(params))
}

func (r *recorder) onRequest(method string, _ json.RawMessage) (any, *rpcError) {
	return r.answer(method)
}

// peer is the far end of a conn: it reads what the conn wrote, line by line,
// and writes raw lines back.
type peer struct {
	in  *bufio.Scanner
	out io.WriteCloser
}

func newPair(t *testing.T, maxBytes int, h handler) (*conn, *peer) {
	t.Helper()
	connIn, peerOut := io.Pipe()
	peerIn, connOut := io.Pipe()
	c := newConn(connIn, connOut, maxBytes, h)
	t.Cleanup(func() { _ = peerOut.Close(); _ = connOut.Close() })
	return c, &peer{in: bufio.NewScanner(peerIn), out: peerOut}
}

func (p *peer) line(t *testing.T) string {
	t.Helper()
	if !p.in.Scan() {
		t.Fatalf("peer: no line: %v", p.in.Err())
	}
	return p.in.Text()
}

func (p *peer) write(s string) { _, _ = io.WriteString(p.out, s+"\n") }

func TestCallsAreMatchedToResponsesById(t *testing.T) {
	c, p := newPair(t, 1<<20, &recorder{})
	type res struct{ V string }
	var a, b res
	errs := make(chan error, 2)
	go func() { errs <- c.call(context.Background(), "first", nil, &a) }()
	first := p.line(t)
	go func() { errs <- c.call(context.Background(), "second", nil, &b) }()
	second := p.line(t)

	// Answer out of order.
	p.write(`{"jsonrpc":"2.0","id":` + idOf(t, second) + `,"result":{"V":"two"}}`)
	p.write(`{"jsonrpc":"2.0","id":` + idOf(t, first) + `,"result":{"V":"one"}}`)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if a.V != "one" || b.V != "two" {
		t.Errorf("got %q and %q; want one and two", a.V, b.V)
	}
}

func TestEveryMessageIsOneLine(t *testing.T) {
	c, p := newPair(t, 1<<20, &recorder{})
	go func() { _ = c.send("note", map[string]string{"text": "two\nlines"}) }()
	got := p.line(t)
	var m message
	if err := json.Unmarshal([]byte(got), &m); err != nil || m.Method != "note" {
		t.Fatalf("line %q is not one complete message: %v", got, err)
	}
}

func TestNotificationsArriveInOrder(t *testing.T) {
	r := &recorder{}
	c, p := newPair(t, 1<<20, r)
	p.write(`{"jsonrpc":"2.0","method":"a","params":1}`)
	p.write(`{"jsonrpc":"2.0","method":"b","params":2}`)
	p.out.Close()
	c.wait()
	if strings.Join(r.notes, ",") != "a 1,b 2" {
		t.Errorf("notes %v; want a then b", r.notes)
	}
}

func TestIncomingRequestsAreAnswered(t *testing.T) {
	r := &recorder{answer: func(method string) (any, *rpcError) {
		if method == "known" {
			return map[string]int{"n": 7}, nil
		}
		return nil, &rpcError{Code: -32601, Message: "method not found: " + method}
	}}
	_, p := newPair(t, 1<<20, r)

	p.write(`{"jsonrpc":"2.0","id":"x1","method":"known"}`)
	if got := p.line(t); got != `{"jsonrpc":"2.0","id":"x1","result":{"n":7}}` {
		t.Errorf("reply %s", got)
	}
	p.write(`{"jsonrpc":"2.0","id":9,"method":"unknown"}`)
	if got := p.line(t); !strings.Contains(got, `"code":-32601`) || !strings.Contains(got, `"id":9`) {
		t.Errorf("reply %s; want -32601 for id 9", got)
	}
}

func TestCancelledCallReturnsAndItsLateResponseIsDropped(t *testing.T) {
	c, p := newPair(t, 1<<20, &recorder{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- c.call(ctx, "slow", nil, nil) }()
	id := idOf(t, p.line(t))
	if err := <-errc; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v; want deadline exceeded", err)
	}

	// The late response must not block the read loop: a later call still works.
	p.write(`{"jsonrpc":"2.0","id":` + id + `,"result":{}}`)
	go func() { errc <- c.call(context.Background(), "next", nil, nil) }()
	p.write(`{"jsonrpc":"2.0","id":` + idOf(t, p.line(t)) + `,"result":{}}`)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestPeerExitFailsWaitingCalls(t *testing.T) {
	c, p := newPair(t, 1<<20, &recorder{})
	errc := make(chan error, 1)
	go func() { errc <- c.call(context.Background(), "never", nil, nil) }()
	p.line(t)
	p.out.Close()
	if err := <-errc; !errors.Is(err, errExited) {
		t.Errorf("err = %v; want errExited", err)
	}
	if err := c.call(context.Background(), "after", nil, nil); !errors.Is(err, errExited) {
		t.Errorf("call after exit: err = %v; want errExited", err)
	}
}

func TestMalformedLineFailsWaitingCalls(t *testing.T) {
	c, p := newPair(t, 1<<20, &recorder{})
	errc := make(chan error, 1)
	go func() { errc <- c.call(context.Background(), "x", nil, nil) }()
	p.line(t)
	p.write(`Loading model...`)
	if err := <-errc; err == nil || !strings.Contains(err.Error(), "Loading model") {
		t.Errorf("err = %v; want one naming the line", err)
	}
}

func TestOverSizeLineFailsWaitingCalls(t *testing.T) {
	c, p := newPair(t, 64, &recorder{})
	errc := make(chan error, 1)
	go func() { errc <- c.call(context.Background(), "x", nil, nil) }()
	p.line(t)
	go p.write(`{"jsonrpc":"2.0","method":"n","params":"` + strings.Repeat("a", 200) + `"}`)
	if err := <-errc; err == nil || !strings.Contains(err.Error(), "64 bytes") {
		t.Errorf("err = %v; want one naming the 64-byte limit", err)
	}
}

func idOf(t *testing.T, line string) string {
	t.Helper()
	var m message
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("line %q: %v", line, err)
	}
	return string(m.ID)
}
