package pluginrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

var ErrClosed = errors.New("plugin RPC peer is closed")

type RemoteError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

func (e *RemoteError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

type Handler func(context.Context, string, json.RawMessage) (any, error)

type message struct {
	ID     uint64          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RemoteError    `json:"error,omitempty"`
}

type response struct {
	result json.RawMessage
	err    error
}

type Peer struct {
	decoder *json.Decoder
	encoder *json.Encoder
	handler Handler

	nextID atomic.Uint64

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[uint64]chan response
	closed  bool
	err     error
	done    chan struct{}
}

func New(reader io.Reader, writer io.Writer, handler Handler) *Peer {
	peer := &Peer{
		decoder: json.NewDecoder(reader),
		encoder: json.NewEncoder(writer),
		handler: handler,
		pending: make(map[uint64]chan response),
		done:    make(chan struct{}),
	}
	go peer.readLoop()
	return peer
}

func (p *Peer) Call(ctx context.Context, method string, params, target any) error {
	if method == "" {
		return errors.New("plugin RPC method is required")
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode plugin RPC params: %w", err)
	}
	id := p.nextID.Add(1)
	waiter := make(chan response, 1)

	p.mu.Lock()
	if p.closed {
		closeErr := p.err
		p.mu.Unlock()
		if closeErr == nil {
			closeErr = ErrClosed
		}
		return closeErr
	}
	p.pending[id] = waiter
	p.mu.Unlock()

	if err := p.write(message{ID: id, Method: method, Params: raw}); err != nil {
		p.removePending(id)
		return err
	}

	select {
	case reply := <-waiter:
		if reply.err != nil {
			return reply.err
		}
		if target == nil || len(reply.result) == 0 ||
			string(reply.result) == "null" {
			return nil
		}
		if err := json.Unmarshal(reply.result, target); err != nil {
			return fmt.Errorf("decode plugin RPC result for %q: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		p.removePending(id)
		return ctx.Err()
	case <-p.done:
		p.removePending(id)
		p.mu.Lock()
		closeErr := p.err
		p.mu.Unlock()
		if closeErr == nil {
			closeErr = ErrClosed
		}
		return closeErr
	}
}

func (p *Peer) Notify(method string, params any) error {
	if method == "" {
		return errors.New("plugin RPC method is required")
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode plugin RPC notification: %w", err)
	}
	return p.write(message{Method: method, Params: raw})
}

func (p *Peer) Done() <-chan struct{} {
	return p.done
}

func (p *Peer) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

func (p *Peer) readLoop() {
	for {
		var incoming message
		if err := p.decoder.Decode(&incoming); err != nil {
			if errors.Is(err, io.EOF) {
				err = ErrClosed
			}
			p.close(err)
			return
		}
		if incoming.Method != "" {
			if incoming.ID == 0 {
				go p.handleNotification(incoming)
			} else {
				go p.handleRequest(incoming)
			}
			continue
		}
		if incoming.ID == 0 {
			continue
		}
		p.mu.Lock()
		waiter, exists := p.pending[incoming.ID]
		if exists {
			delete(p.pending, incoming.ID)
		}
		p.mu.Unlock()
		if !exists {
			continue
		}
		if incoming.Error != nil {
			waiter <- response{err: incoming.Error}
		} else {
			waiter <- response{result: incoming.Result}
		}
	}
}

func (p *Peer) handleNotification(incoming message) {
	if p.handler == nil {
		return
	}
	_, _ = p.handler(context.Background(), incoming.Method, incoming.Params)
}

func (p *Peer) handleRequest(incoming message) {
	if p.handler == nil {
		_ = p.write(message{
			ID: incoming.ID,
			Error: &RemoteError{
				Code:    "method_not_found",
				Message: "plugin RPC handler is unavailable",
			},
		})
		return
	}
	result, err := p.handler(context.Background(), incoming.Method, incoming.Params)
	if err != nil {
		var remote *RemoteError
		if !errors.As(err, &remote) {
			remote = &RemoteError{
				Code:    "remote_error",
				Message: err.Error(),
			}
		}
		_ = p.write(message{
			ID:    incoming.ID,
			Error: remote,
		})
		return
	}
	raw, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		_ = p.write(message{
			ID: incoming.ID,
			Error: &RemoteError{
				Code:    "encode_result",
				Message: marshalErr.Error(),
			},
		})
		return
	}
	_ = p.write(message{ID: incoming.ID, Result: raw})
}

func (p *Peer) write(outgoing message) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	p.mu.Lock()
	closed := p.closed
	closeErr := p.err
	p.mu.Unlock()
	if closed {
		if closeErr == nil {
			closeErr = ErrClosed
		}
		return closeErr
	}
	if err := p.encoder.Encode(outgoing); err != nil {
		p.close(err)
		return fmt.Errorf("write plugin RPC message: %w", err)
	}
	return nil
}

func (p *Peer) removePending(id uint64) {
	p.mu.Lock()
	delete(p.pending, id)
	p.mu.Unlock()
}

func (p *Peer) close(err error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.err = err
	pending := p.pending
	p.pending = make(map[uint64]chan response)
	close(p.done)
	p.mu.Unlock()

	if err == nil {
		err = ErrClosed
	}
	for _, waiter := range pending {
		waiter <- response{err: err}
	}
}
