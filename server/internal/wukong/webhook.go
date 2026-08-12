package wukong

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	hookpb "github.com/linli/im/server/internal/wukong/hookpb"
	"google.golang.org/grpc"
)

const (
	EventMessageOffline = "msg.offline"
	EventMessageNotify  = "msg.notify"
	EventOnlineStatus   = "user.onlinestatus"
	EventMessageStream  = "msg.stream"
)

type WebhookEvent struct {
	ID         string
	EventType  string
	Payload    json.RawMessage
	ReceivedAt time.Time
}

type WebhookEventStore interface {
	PutWukongWebhookEvent(context.Context, WebhookEvent) (bool, error)
}

type WebhookHandler struct {
	hookpb.UnimplementedWebhookServiceServer
	store WebhookEventStore
	now   func() time.Time
}

func NewWebhookHandler(store WebhookEventStore) (*WebhookHandler, error) {
	if store == nil {
		return nil, errors.New("WuKongIM webhook store is required")
	}
	return &WebhookHandler{store: store, now: time.Now}, nil
}

func (h *WebhookHandler) SendWebhook(ctx context.Context, request *hookpb.EventReq) (*hookpb.EventResp, error) {
	events, err := ExpandWebhook(request.GetEvent(), request.GetData(), h.now().UTC())
	if err != nil {
		return &hookpb.EventResp{Status: hookpb.EventStatus_Error, Data: []byte(err.Error())}, nil
	}
	for _, event := range events {
		if _, err = h.store.PutWukongWebhookEvent(ctx, event); err != nil {
			return nil, fmt.Errorf("persist WuKongIM webhook: %w", err)
		}
	}
	return &hookpb.EventResp{Status: hookpb.EventStatus_Success}, nil
}

func ExpandWebhook(eventType string, data []byte, receivedAt time.Time) ([]WebhookEvent, error) {
	eventType = strings.TrimSpace(eventType)
	switch eventType {
	case EventMessageOffline, EventMessageNotify, EventOnlineStatus, EventMessageStream:
	default:
		return nil, fmt.Errorf("unsupported WuKongIM webhook event %q", eventType)
	}
	if !json.Valid(data) {
		return nil, errors.New("WuKongIM webhook data is not valid JSON")
	}
	items := []json.RawMessage{append(json.RawMessage(nil), data...)}
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, fmt.Errorf("decode WuKongIM webhook array: %w", err)
		}
	}
	events := make([]WebhookEvent, 0, len(items))
	for _, item := range items {
		compact := bytes.NewBuffer(nil)
		if err := json.Compact(compact, item); err != nil {
			return nil, err
		}
		payload := append(json.RawMessage(nil), compact.Bytes()...)
		hash := sha256.Sum256(append(append([]byte(eventType), 0), payload...))
		events = append(events, WebhookEvent{
			ID: "wke_" + hex.EncodeToString(hash[:]), EventType: eventType,
			Payload: payload, ReceivedAt: receivedAt,
		})
	}
	return events, nil
}

type WebhookGRPCServer struct {
	listener net.Listener
	server   *grpc.Server
}

func ListenWebhookGRPC(addr string, store WebhookEventStore) (*WebhookGRPCServer, error) {
	handler, err := NewWebhookHandler(store)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	server := grpc.NewServer(grpc.MaxRecvMsgSize(16 << 20))
	hookpb.RegisterWebhookServiceServer(server, handler)
	return &WebhookGRPCServer{listener: listener, server: server}, nil
}

func (s *WebhookGRPCServer) Serve() error {
	err := s.server.Serve(s.listener)
	if errors.Is(err, grpc.ErrServerStopped) {
		return nil
	}
	return err
}
func (s *WebhookGRPCServer) Stop() { s.server.GracefulStop() }

type MemoryWebhookStore struct {
	mu     sync.Mutex
	events map[string]WebhookEvent
}

func NewMemoryWebhookStore() *MemoryWebhookStore {
	return &MemoryWebhookStore{events: map[string]WebhookEvent{}}
}

func (s *MemoryWebhookStore) PutWukongWebhookEvent(_ context.Context, event WebhookEvent) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.events[event.ID]; exists {
		return false, nil
	}
	s.events[event.ID] = event
	return true, nil
}

func (s *MemoryWebhookStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}
