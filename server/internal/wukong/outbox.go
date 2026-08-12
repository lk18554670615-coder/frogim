package wukong

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"
)

const (
	OperationFriendAllow      = "friend.allow"
	OperationFriendRemove     = "friend.remove"
	OperationFriendBlock      = "friend.block"
	OperationFriendUnblock    = "friend.unblock"
	OperationChannelReconcile = "channel.reconcile"
	OperationCallEvent        = "call.event"
	OperationBusinessEvent    = "business.event"
	OperationStoredMessage    = "message.store"
	OperationSystemUIDAdd     = "system_uid.add"
	OperationSystemUIDRemove  = "system_uid.remove"
)

type OutboxItem struct {
	ID            int64
	Operation     string
	AggregateType string
	AggregateID   string
	Payload       json.RawMessage
	Attempts      int
}

type OutboxStore interface {
	ClaimWukongOutbox(context.Context, int) ([]OutboxItem, error)
	CompleteWukongOutbox(context.Context, int64) error
	FailWukongOutbox(context.Context, int64, string, bool) error
}

type ChannelSnapshot struct {
	Cursor        string
	ChannelID     string
	ChannelType   uint8
	Large         int
	Ban           int
	Disband       int
	SendBan       int
	AllowStranger int
	Subscribers   []string
	Allowlist     []string
	Denylist      []string
}

type ChannelSnapshotStore interface {
	LoadWukongChannelSnapshot(context.Context, string, uint8) (ChannelSnapshot, error)
}

type OutboxWorker struct {
	store    OutboxStore
	client   *Client
	interval time.Duration
	batch    int
}

func NewOutboxWorker(store OutboxStore, client *Client) (*OutboxWorker, error) {
	if store == nil || client == nil {
		return nil, errors.New("WuKongIM outbox requires a store and client")
	}
	return &OutboxWorker{store: store, client: client, interval: 100 * time.Millisecond, batch: 100}, nil
}

func (w *OutboxWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		if err := w.runOnce(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("WuKongIM outbox cycle failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *OutboxWorker) runOnce(ctx context.Context) error {
	items, err := w.store.ClaimWukongOutbox(ctx, w.batch)
	if err != nil {
		return err
	}
	for _, item := range items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		processErr, retry := w.process(ctx, item)
		if processErr == nil {
			if err = w.store.CompleteWukongOutbox(ctx, item.ID); err != nil {
				return err
			}
			continue
		}
		if err = w.store.FailWukongOutbox(ctx, item.ID, processErr.Error(), retry); err != nil {
			return err
		}
	}
	return nil
}

type friendOperationPayload struct {
	UserID   string `json:"user_id"`
	FriendID string `json:"friend_id"`
}

type channelOperationPayload struct {
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
}

type CommandPayload struct {
	Recipients []string       `json:"recipients"`
	Event      string         `json:"event"`
	Param      map[string]any `json:"param"`
}

type CallEventPayload = CommandPayload

type systemUIDOperationPayload struct {
	UIDs []string `json:"uids"`
}

func (w *OutboxWorker) process(ctx context.Context, item OutboxItem) (error, bool) {
	if item.Operation == OperationSystemUIDAdd || item.Operation == OperationSystemUIDRemove {
		var payload systemUIDOperationPayload
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			return err, false
		}
		if item.Operation == OperationSystemUIDAdd {
			return callClientError(w.client.AddSystemUIDs(ctx, payload.UIDs))
		}
		return callClientError(w.client.RemoveSystemUIDs(ctx, payload.UIDs))
	}
	if item.Operation == OperationStoredMessage {
		var payload StoredMessageRequest
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			return err, false
		}
		_, err := w.client.SendStoredMessage(ctx, payload)
		return callClientError(err)
	}
	if item.Operation == OperationCallEvent || item.Operation == OperationBusinessEvent {
		var payload CommandPayload
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			return err, false
		}
		isCall := strings.HasPrefix(payload.Event, "call.")
		if item.Operation == OperationCallEvent && !isCall {
			return errors.New("invalid call event command"), false
		}
		if item.Operation == OperationBusinessEvent && (isCall || payload.Event == "message.created") {
			return errors.New("invalid business event command"), false
		}
		if version, ok := payload.Param["schemaVersion"].(float64); !ok || int(version) != 1 || payload.Param["event"] != payload.Event {
			return errors.New("invalid command schema"), false
		}
		return callClientError(w.client.SendCommand(ctx, payload.Recipients, payload.Event, payload.Param))
	}
	if item.Operation == OperationChannelReconcile {
		var payload channelOperationPayload
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			return err, false
		}
		snapshots, ok := w.store.(ChannelSnapshotStore)
		if !ok {
			return errors.New("WuKongIM channel snapshot store is unavailable"), false
		}
		snapshot, err := snapshots.LoadWukongChannelSnapshot(ctx, strings.TrimSpace(payload.ChannelID), payload.ChannelType)
		if err != nil {
			return err, true
		}
		return callClientError(applyChannelSnapshot(ctx, w.client, snapshot))
	}
	var payload friendOperationPayload
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		return err, false
	}
	payload.UserID, payload.FriendID = strings.TrimSpace(payload.UserID), strings.TrimSpace(payload.FriendID)
	if payload.UserID == "" || payload.FriendID == "" || payload.UserID == payload.FriendID {
		return errors.New("invalid friend operation payload"), false
	}

	switch item.Operation {
	case OperationFriendAllow:
		if err := w.client.AddAllowlist(ctx, payload.UserID, ChannelPerson, []string{payload.FriendID}); err != nil {
			return callClientError(err)
		}
		return callClientError(w.client.AddAllowlist(ctx, payload.FriendID, ChannelPerson, []string{payload.UserID}))
	case OperationFriendRemove:
		if err := w.client.RemoveAllowlist(ctx, payload.UserID, ChannelPerson, []string{payload.FriendID}); err != nil {
			return callClientError(err)
		}
		return callClientError(w.client.RemoveAllowlist(ctx, payload.FriendID, ChannelPerson, []string{payload.UserID}))
	case OperationFriendBlock:
		if err := w.client.AddDenylist(ctx, payload.UserID, ChannelPerson, []string{payload.FriendID}); err != nil {
			return callClientError(err)
		}
		if err := w.client.RemoveAllowlist(ctx, payload.UserID, ChannelPerson, []string{payload.FriendID}); err != nil {
			return callClientError(err)
		}
		return callClientError(w.client.RemoveAllowlist(ctx, payload.FriendID, ChannelPerson, []string{payload.UserID}))
	case OperationFriendUnblock:
		return callClientError(w.client.RemoveDenylist(ctx, payload.UserID, ChannelPerson, []string{payload.FriendID}))
	default:
		return errors.New("unsupported WuKongIM outbox operation"), false
	}
}

func applyChannelSnapshot(ctx context.Context, client *Client, snapshot ChannelSnapshot) error {
	if err := client.UpsertChannel(ctx, ChannelRequest{
		ChannelID: snapshot.ChannelID, ChannelType: snapshot.ChannelType,
		Large: snapshot.Large, Ban: snapshot.Ban, Disband: snapshot.Disband,
		SendBan: snapshot.SendBan, AllowStranger: snapshot.AllowStranger,
		Reset: 1, Subscribers: snapshot.Subscribers,
	}); err != nil {
		return err
	}
	if err := client.SetAllowlist(ctx, snapshot.ChannelID, snapshot.ChannelType, snapshot.Allowlist); err != nil {
		return err
	}
	return client.SetDenylist(ctx, snapshot.ChannelID, snapshot.ChannelType, snapshot.Denylist)
}

func callClientError(err error) (error, bool) {
	if err == nil {
		return nil, false
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode >= 400 && httpErr.StatusCode < 500 && httpErr.StatusCode != 429 {
		return err, false
	}
	return err, true
}
