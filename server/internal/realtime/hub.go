package realtime

import (
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/netutil"
)

type Presence struct {
	UserID      string `json:"userId"`
	Connections int    `json:"connections"`
}

type Envelope struct {
	Version     int             `json:"version"`
	RequestID   string          `json:"requestId,omitempty"`
	Type        string          `json:"type"`
	UserSyncSeq int64           `json:"userSyncSeq,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	Error       *Error          `json:"error,omitempty"`
}
type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}
type Client struct {
	uid, ip     string
	conn        *websocket.Conn
	send        chan []byte
	expiresAt   time.Time
	frameWindow time.Time
	frames      int
}
type Hub struct {
	mu                   sync.RWMutex
	clients              map[string]map[*Client]struct{}
	byIP                 map[string]int
	app                  *app.App
	allowedOrigins       map[string]bool
	maxPerUser, maxPerIP int
	trustProxy           bool
}

func New(a *app.App, origins []string, maxPerUser, maxPerIP int, trustProxy bool) *Hub {
	if maxPerUser <= 0 {
		maxPerUser = 5
	}
	if maxPerIP <= 0 {
		maxPerIP = 20
	}
	allowed := make(map[string]bool, len(origins))
	for _, origin := range origins {
		allowed[origin] = true
	}
	h := &Hub{clients: map[string]map[*Client]struct{}{}, byIP: map[string]int{}, app: a, allowedOrigins: allowed, maxPerUser: maxPerUser, maxPerIP: maxPerIP, trustProxy: trustProxy}
	a.SetEventSink(h.Publish)
	return h
}

func (h *Hub) Publish(userIDs []string, typ string, payload any) {
	raw, _ := json.Marshal(payload)
	env, _ := json.Marshal(Envelope{Version: 1, Type: typ, Payload: raw})
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, uid := range userIDs {
		for c := range h.clients[uid] {
			select {
			case c.send <- env:
			default: // slow consumers recover from the durable sync cursor
			}
		}
	}
}

func (h *Hub) Presence() []Presence {
	h.mu.RLock()
	items := make([]Presence, 0, len(h.clients))
	for userID, connections := range h.clients {
		items = append(items, Presence{UserID: userID, Connections: len(connections)})
	}
	h.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool { return items[i].UserID < items[j].UserID })
	return items
}

func (h *Hub) Serve(w http.ResponseWriter, r *http.Request, uid string, expiresAt time.Time) {
	origin := r.Header.Get("Origin")
	if origin != "" && !h.allowedOrigins[origin] {
		http.Error(w, "origin is not allowed", http.StatusForbidden)
		return
	}
	ip := netutil.ClientIP(r, h.trustProxy)
	h.mu.Lock()
	if len(h.clients[uid]) >= h.maxPerUser || h.byIP[ip] >= h.maxPerIP {
		h.mu.Unlock()
		http.Error(w, "connection budget exceeded", http.StatusTooManyRequests)
		return
	}
	h.mu.Unlock()
	upgrader := websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096, CheckOrigin: func(req *http.Request) bool {
		o := req.Header.Get("Origin")
		return o == "" || h.allowedOrigins[o]
	}}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &Client{uid: uid, ip: ip, conn: conn, send: make(chan []byte, 128), expiresAt: expiresAt, frameWindow: time.Now().Add(time.Minute)}
	h.mu.Lock()
	if len(h.clients[uid]) >= h.maxPerUser || h.byIP[ip] >= h.maxPerIP {
		h.mu.Unlock()
		_ = conn.Close()
		return
	}
	if h.clients[uid] == nil {
		h.clients[uid] = map[*Client]struct{}{}
	}
	h.clients[uid][c] = struct{}{}
	h.byIP[ip]++
	h.mu.Unlock()
	h.app.Metrics.WSConnections.Add(1)
	go h.writer(c)
	h.send(c, "session.ready", map[string]any{"heartbeatIntervalMs": 25000, "protocolVersion": 1})
	h.reader(c)
	h.remove(c)
}
func (h *Hub) remove(c *Client) {
	h.mu.Lock()
	if _, ok := h.clients[c.uid][c]; ok {
		delete(h.clients[c.uid], c)
		h.byIP[c.ip]--
		if h.byIP[c.ip] <= 0 {
			delete(h.byIP, c.ip)
		}
		close(c.send)
	}
	if len(h.clients[c.uid]) == 0 {
		delete(h.clients, c.uid)
	}
	h.mu.Unlock()
	h.app.Metrics.WSConnections.Add(-1)
	_ = c.conn.Close()
}
func (h *Hub) send(c *Client, typ string, p any) {
	raw, _ := json.Marshal(p)
	b, _ := json.Marshal(Envelope{Version: 1, Type: typ, Payload: raw})
	select {
	case c.send <- b:
	default:
	}
}
func (h *Hub) reply(c *Client, req Envelope, typ string, p any, e *Error) {
	raw, _ := json.Marshal(p)
	b, _ := json.Marshal(Envelope{Version: 1, RequestID: req.RequestID, Type: typ, Payload: raw, Error: e})
	select {
	case c.send <- b:
	default:
	}
}
func (h *Hub) writer(c *Client) {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if c.conn.WriteMessage(websocket.TextMessage, msg) != nil {
				_ = c.conn.Close()
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if c.conn.WriteMessage(websocket.PingMessage, nil) != nil {
				_ = c.conn.Close()
				return
			}
		}
	}
}
func (h *Hub) reader(c *Client) {
	c.conn.SetReadLimit(1024 * 1024)
	setReadDeadline(c)
	c.conn.SetPongHandler(func(string) error { setReadDeadline(c); return nil })
	for {
		if time.Now().After(c.expiresAt) {
			return
		}
		var req Envelope
		if c.conn.ReadJSON(&req) != nil {
			return
		}
		if req.Version != 1 {
			h.reply(c, req, "error", nil, &Error{Code: "UNSUPPORTED_VERSION", Message: "protocol version must be 1"})
			continue
		}
		now := time.Now()
		if now.After(c.frameWindow) {
			c.frameWindow = now.Add(time.Minute)
			c.frames = 0
		}
		c.frames++
		if c.frames > 120 {
			h.reply(c, req, "error", nil, &Error{Code: "RATE_LIMITED", Message: "too many realtime frames", Retryable: true})
			return
		}
		u, err := h.app.User(c.uid)
		if err != nil || u.Banned {
			h.reply(c, req, "error", nil, &Error{Code: "FORBIDDEN", Message: "account unavailable"})
			return
		}
		switch req.Type {
		case "ping":
			h.reply(c, req, "pong", map[string]any{"serverTime": time.Now()}, nil)
		case "message.send":
			var p struct {
				ConversationID, ClientMsgID, MessageType, ReplyToID string
				Body                                                map[string]any
			}
			if json.Unmarshal(req.Payload, &p) != nil {
				h.reply(c, req, "error", nil, &Error{Code: "BAD_PAYLOAD", Message: "invalid payload"})
				continue
			}
			m, duplicate, err := h.app.SendMessage(c.uid, p.ConversationID, p.ClientMsgID, p.MessageType, p.Body, p.ReplyToID)
			if err != nil {
				h.reply(c, req, "error", nil, appError(err))
				continue
			}
			h.reply(c, req, "message.ack", map[string]any{"message": m, "duplicate": duplicate}, nil)
		case "message.read":
			var p struct {
				ConversationID string
				Seq            int64
			}
			_ = json.Unmarshal(req.Payload, &p)
			if err := h.app.Read(c.uid, p.ConversationID, p.Seq); err != nil {
				h.reply(c, req, "error", nil, appError(err))
			} else {
				h.reply(c, req, "message.read.ack", p, nil)
			}
		case "typing":
			var p struct {
				ConversationID string
				Typing         bool
			}
			_ = json.Unmarshal(req.Payload, &p)
			if !h.app.CanAccess(c.uid, p.ConversationID) {
				h.reply(c, req, "error", nil, &Error{Code: "FORBIDDEN", Message: "not a conversation member"})
				continue
			}
			h.app.SetTyping(c.uid, p.ConversationID, p.Typing)
			h.reply(c, req, "typing.ack", p, nil)
		case "call.offer", "call.answer", "call.ice", "call.end", "call.signal.received":
			var p map[string]any
			if json.Unmarshal(req.Payload, &p) != nil {
				h.reply(c, req, "error", nil, &Error{Code: "BAD_PAYLOAD", Message: "invalid payload"})
				continue
			}
			cid, _ := p["conversationId"].(string)
			callID, _ := p["callId"].(string)
			if cid == "" || callID == "" {
				h.reply(c, req, "error", nil, &Error{Code: "BAD_PAYLOAD", Message: "conversationId and callId are required"})
				continue
			}
			if err := h.app.SignalCall(c.uid, cid, req.Type, p); err != nil {
				h.reply(c, req, "error", nil, appError(err))
				continue
			}
			h.reply(c, req, req.Type+".ack", map[string]any{"callId": callID}, nil)
		case "sync":
			var p struct {
				After int64
				Limit int
			}
			_ = json.Unmarshal(req.Payload, &p)
			events, cursor, more := h.app.Sync(c.uid, p.After, p.Limit)
			h.reply(c, req, "sync.result", map[string]any{"events": events, "cursor": cursor, "hasMore": more}, nil)
		default:
			h.reply(c, req, "error", nil, &Error{Code: "UNKNOWN_TYPE", Message: "unknown envelope type"})
		}
	}
}
func setReadDeadline(c *Client) {
	deadline := time.Now().Add(70 * time.Second)
	if c.expiresAt.Before(deadline) {
		deadline = c.expiresAt
	}
	_ = c.conn.SetReadDeadline(deadline)
}
func appError(err error) *Error {
	code := "INTERNAL"
	retry := false
	switch err {
	case app.ErrInvalid:
		code = "INVALID_ARGUMENT"
	case app.ErrForbidden:
		code = "FORBIDDEN"
	case app.ErrNotFound:
		code = "NOT_FOUND"
	case app.ErrConflict:
		code = "CONFLICT"
	default:
		retry = true
	}
	return &Error{Code: code, Message: err.Error(), Retryable: retry}
}
