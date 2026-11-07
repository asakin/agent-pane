package jsonrpc

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type RequestHandlerFunc = func(params []byte) (result any, err error)

type Host struct {
	logger         *slog.Logger
	mu             sync.Mutex
	handlers       map[string]RequestHandlerFunc
	clientChannels map[string]chan JSONRPCResponse

	in  io.Reader
	out io.Writer
	// queue feeds the one goroutine that writes to out, so no caller ever
	// blocks on the native pipe or on another caller's write.
	queue       chan *outbound
	startWriter sync.Once
}

// Outbound message states. A message is written only if the writer claims it
// before its sender withdraws it.
const (
	outQueued int32 = iota
	outWriting
	outWithdrawn
)

type outbound struct {
	frame   []byte
	state   atomic.Int32
	written chan error
}

func (h *Host) HandleRequest(method string, handler RequestHandlerFunc) {
	h.handlers[method] = handler
}

func NewHost(logger *slog.Logger) *Host {
	return &Host{
		logger:         logger,
		handlers:       make(map[string]RequestHandlerFunc),
		clientChannels: make(map[string]chan JSONRPCResponse),
		in:             os.Stdin,
		out:            os.Stdout,
		queue:          make(chan *outbound, 64),
	}
}

func (h *Host) Listen() error {
	for {
		lengthBytes := make([]byte, 4)
		if _, err := io.ReadFull(h.in, lengthBytes); err != nil {
			if err == io.EOF {
				h.logger.Info("EOF reached, stopping listener")
				return nil
			}

			h.logger.Error("failed to read message length", "error", err)
			return err
		}

		length := binary.LittleEndian.Uint32(lengthBytes)

		msgBytes := make([]byte, length)
		if _, err := io.ReadFull(h.in, msgBytes); err != nil {
			h.logger.Error("failed to read message", "error", err)
			continue
		}

		var msg map[string]any
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			h.logger.Error("failed to unmarshal message", "error", err)
			continue
		}

		if _, ok := msg["jsonrpc"]; !ok {
			h.logger.Error("invalid message format, missing jsonrpc field")
			continue
		}

		_, containsResult := msg["result"]
		_, containsError := msg["error"]

		if containsResult || containsError {
			var response JSONRPCResponse
			if err := json.Unmarshal(msgBytes, &response); err != nil {
				h.logger.Error("failed to unmarshal response", "error", err)
				return err
			}

			h.mu.Lock()
			responseChan, ok := h.clientChannels[response.ID]
			if !ok {
				h.logger.Error("no client found")
				h.mu.Unlock()
				continue
			}

			responseChan <- response
			delete(h.clientChannels, response.ID)
			h.mu.Unlock()
			continue
		}

		var request JSONRPCRequest
		if err := json.Unmarshal(msgBytes, &request); err != nil {
			h.logger.Error("failed to unmarshal request", "error", err)
			return err
		}

		if request.ID == "" {
			continue
		}

		handler, ok := h.handlers[request.Method]
		if !ok {
			h.logger.Error("no handler found for request", "method", request.Method)

			errorBytes, err := json.Marshal(map[string]any{
				"code":    -32601,
				"message": fmt.Sprintf("Method not found: %s", request.Method),
			})
			if err != nil {
				h.logger.Error("failed to marshal error response", "error", err)
				continue
			}

			h.writeMessage(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      request.ID,
				Error:   errorBytes,
			})
			continue
		}

		go func() {
			res, err := handler(request.Params)
			if err != nil {
				h.logger.Error("failed to handle request", "method", request.Method, "err", err)
				errorBytes, err := json.Marshal(map[string]any{
					"code":    -32603,
					"message": fmt.Sprintf("Internal error: %s", err),
				})
				if err != nil {
					h.logger.Error("failed to marshal error response", "error", err)
					return
				}

				h.writeMessage(JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      request.ID,
					Error:   errorBytes,
				})
				return
			}

			resBytes, err := json.Marshal(res)
			if err != nil {
				h.logger.Error("failed to marshal result", "error", err)
				return
			}

			response := JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      request.ID,
				Result:  resBytes,
				Error:   nil,
			}

			if err := h.writeMessage(response); err != nil {
				h.logger.Error("failed to write response", "error", err)
				return
			}
		}()
	}
}

func (h *Host) SendRequest(request JSONRPCRequest) (JSONRPCResponse, error) {
	return h.SendRequestTimeout(request, 5*time.Second)
}

// SendRequestTimeout sends a request to the extension and waits up to timeout
// for its answer. A late answer is dropped without blocking the listen loop.
func (h *Host) SendRequestTimeout(request JSONRPCRequest, timeout time.Duration) (JSONRPCResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return h.SendRequestContext(ctx, request)
}

// SendRequestContext sends a request to the extension and waits for its
// answer until ctx ends. The wait covers queueing and writing as well as the
// answer: a request still queued behind a stalled native pipe when ctx ends
// is withdrawn and never written.
func (h *Host) SendRequestContext(ctx context.Context, request JSONRPCRequest) (JSONRPCResponse, error) {
	h.mu.Lock()
	responseChan := make(chan JSONRPCResponse, 1)
	h.clientChannels[request.ID] = responseChan
	h.mu.Unlock()
	forget := func() {
		h.mu.Lock()
		delete(h.clientChannels, request.ID)
		h.mu.Unlock()
	}

	o, err := h.enqueue(ctx, request)
	if err != nil {
		forget()
		return JSONRPCResponse{}, fmt.Errorf("failed to write request: %w", err)
	}
	written := o.written
	for {
		select {
		case response := <-responseChan:
			return response, nil
		case err := <-written:
			if err != nil {
				forget()
				return JSONRPCResponse{}, fmt.Errorf("failed to write request: %w", err)
			}
			written = nil
		case <-ctx.Done():
			o.state.CompareAndSwap(outQueued, outWithdrawn)
			forget()
			return JSONRPCResponse{}, fmt.Errorf("no response: %w", ctx.Err())
		}
	}
}

// enqueue hands one message to the writer, waiting for room in the queue
// until ctx ends.
func (h *Host) enqueue(ctx context.Context, data any) (*outbound, error) {
	msg, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	frame := make([]byte, 4, 4+len(msg))
	binary.LittleEndian.PutUint32(frame, uint32(len(msg)))
	o := &outbound{frame: append(frame, msg...), written: make(chan error, 1)}
	h.startWriter.Do(func() { go h.writeLoop() })
	select {
	case h.queue <- o:
		return o, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// writeLoop writes queued messages in order, skipping withdrawn ones. If the
// browser stops reading the pipe, this goroutine blocks, and senders give up
// at their own deadlines.
func (h *Host) writeLoop() {
	for o := range h.queue {
		if !o.state.CompareAndSwap(outQueued, outWriting) {
			continue
		}
		_, err := h.out.Write(o.frame)
		o.written <- err
	}
}

// writeMessage writes a response to the extension and waits until it is
// written.
func (h *Host) writeMessage(data any) error {
	o, err := h.enqueue(context.Background(), data)
	if err != nil {
		return err
	}
	return <-o.written
}
