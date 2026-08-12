package wukong

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type UserAccessSnapshot struct {
	UID       string
	Allowlist []string
	Denylist  []string
}

type ReconcileStore interface {
	ListWukongUserAccess(context.Context, string, int) ([]UserAccessSnapshot, error)
	ListWukongChannels(context.Context, string, int) ([]ChannelSnapshot, error)
}

type Reconciler struct {
	store                  ReconcileStore
	client                 *Client
	batch                  int
	pageInterval           time.Duration
	cycleInterval          time.Duration
	userCursor             string
	channelCursor          string
	usersDone              bool
	channelsDone           bool
	maxItemAttempts        int
	channelFailureCursor   string
	channelFailureAttempts int
	cycleFailure           error
}

func NewReconciler(store ReconcileStore, client *Client) (*Reconciler, error) {
	if store == nil || client == nil {
		return nil, errors.New("WuKongIM reconciler requires a store and client")
	}
	return &Reconciler{store: store, client: client, batch: 100, pageInterval: time.Second, cycleInterval: 10 * time.Minute, maxItemAttempts: 3}, nil
}

func (r *Reconciler) Run(ctx context.Context) {
	delay := 30 * time.Second
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		complete, err := r.runPage(ctx)
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("WuKongIM reconcile page failed", "error", err)
			}
			if complete {
				delay = r.cycleInterval
			} else {
				delay = minDuration(30*time.Second, r.cycleInterval)
			}
			continue
		}
		if complete {
			delay = r.cycleInterval
		} else {
			delay = r.pageInterval
		}
	}
}

func (r *Reconciler) runPage(ctx context.Context) (bool, error) {
	if !r.usersDone {
		items, err := r.store.ListWukongUserAccess(ctx, r.userCursor, r.batch)
		if err != nil {
			return false, err
		}
		for _, item := range items {
			if err = r.client.SetAllowlist(ctx, item.UID, ChannelPerson, item.Allowlist); err != nil {
				return false, err
			}
			if err = r.client.SetDenylist(ctx, item.UID, ChannelPerson, item.Denylist); err != nil {
				return false, err
			}
			r.userCursor = item.UID
		}
		r.usersDone = len(items) < r.batch
	}

	if !r.channelsDone {
		items, err := r.store.ListWukongChannels(ctx, r.channelCursor, r.batch)
		if err != nil {
			return false, err
		}
		for _, item := range items {
			if err = applyChannelSnapshot(ctx, r.client, item); err != nil {
				if permanentReconcileError(err) {
					r.cycleFailure = errors.Join(r.cycleFailure, fmt.Errorf("channel reconcile deferred type=%d id=%q: %w", item.ChannelType, item.ChannelID, err))
					r.channelCursor = item.Cursor
					r.channelFailureCursor = ""
					r.channelFailureAttempts = 0
					continue
				}
				if r.channelFailureCursor == item.Cursor {
					r.channelFailureAttempts++
				} else {
					r.channelFailureCursor = item.Cursor
					r.channelFailureAttempts = 1
				}
				if r.channelFailureAttempts < r.maxItemAttempts {
					return false, err
				}
				// A permanently invalid snapshot must not block every later channel.
				// Advancing only defers it until the next full cycle; it is never
				// recorded as successful and remains visible through the error log.
				r.cycleFailure = errors.Join(r.cycleFailure, err)
				r.channelCursor = item.Cursor
				r.channelFailureCursor = ""
				r.channelFailureAttempts = 0
				continue
			}
			r.channelCursor = item.Cursor
			if r.channelFailureCursor == item.Cursor {
				r.channelFailureCursor = ""
				r.channelFailureAttempts = 0
			}
		}
		r.channelsDone = len(items) < r.batch
	}

	if r.usersDone && r.channelsDone {
		cycleFailure := r.cycleFailure
		r.userCursor, r.channelCursor = "", ""
		r.usersDone, r.channelsDone = false, false
		r.channelFailureCursor = ""
		r.channelFailureAttempts = 0
		r.cycleFailure = nil
		return true, cycleFailure
	}
	return false, nil
}

func permanentReconcileError(err error) bool {
	var httpErr *HTTPError
	return errors.As(err, &httpErr) &&
		httpErr.StatusCode >= http.StatusBadRequest &&
		httpErr.StatusCode < http.StatusInternalServerError &&
		httpErr.StatusCode != http.StatusRequestTimeout &&
		httpErr.StatusCode != http.StatusTooManyRequests
}

func minDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}
