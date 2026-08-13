package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/linli/im/server/internal/wukong"
)

func TestWukongOutboxRetriesTransientFailureAcrossWorkerRestart(t *testing.T) {
	p := newIsolatedWukongStore(t)
	ctx := t.Context()
	aggregateID := fmt.Sprintf("fault-message-%d", time.Now().UnixNano())
	clientMsgNo := "stored-" + aggregateID
	payload, err := json.Marshal(wukong.StoredMessageRequest{
		ClientMsgNo: clientMsgNo,
		FromUID:     "system",
		ChannelID:   "group-fault",
		ChannelType: wukong.ChannelGroup,
		Payload: map[string]any{
			"type":          wukong.ContentTypeSystemEvent,
			"schemaVersion": 1,
			"event":         "fault.recovered",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var outboxID int64
	if err = p.pool.QueryRow(ctx, `
		INSERT INTO im_wukong_outbox(idempotency_key,operation,aggregate_type,aggregate_id,payload)
		VALUES($1,$2,'fault_test',$3,$4) RETURNING id
	`, "fault:"+aggregateID, wukong.OperationStoredMessage, aggregateID, payload).Scan(&outboxID); err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/message/send" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		attempt := requests.Add(1)
		if attempt == 1 {
			http.Error(w, "injected outage", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": http.StatusOK,
			"data": map[string]any{
				"message_id":    901,
				"client_msg_no": clientMsgNo,
			},
		})
	}))
	defer server.Close()
	client, err := wukong.NewClient(wukong.Config{
		APIURL: server.URL, ManagerURL: server.URL, ManagerToken: "fault-test", MaxRetries: 0,
	})
	if err != nil {
		t.Fatal(err)
	}

	worker, err := wukong.NewOutboxWorker(p, client)
	if err != nil {
		t.Fatal(err)
	}
	firstCtx, cancelFirst := context.WithCancel(ctx)
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		worker.Run(firstCtx)
	}()
	waitForWukongOutboxState(t, p, outboxID, "pending", 1)
	cancelFirst()
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("first outbox worker did not stop")
	}
	if _, err = p.pool.Exec(ctx, `UPDATE im_wukong_outbox SET available_at=now() WHERE id=$1`, outboxID); err != nil {
		t.Fatal(err)
	}

	restarted, err := wukong.NewOutboxWorker(p, client)
	if err != nil {
		t.Fatal(err)
	}
	secondCtx, cancelSecond := context.WithCancel(ctx)
	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		restarted.Run(secondCtx)
	}()
	waitForWukongOutboxState(t, p, outboxID, "completed", 2)
	cancelSecond()
	select {
	case <-secondDone:
	case <-time.After(2 * time.Second):
		t.Fatal("restarted outbox worker did not stop")
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("WuKong requests=%d want=2", got)
	}
	var lastError string
	var completedAt *time.Time
	if err = p.pool.QueryRow(ctx, `SELECT last_error,completed_at FROM im_wukong_outbox WHERE id=$1`, outboxID).Scan(&lastError, &completedAt); err != nil {
		t.Fatal(err)
	}
	if lastError != "" || completedAt == nil {
		t.Fatalf("lastError=%q completedAt=%v", lastError, completedAt)
	}
}

func TestWukongOutboxReclaimsExpiredLeaseOnceAcrossConcurrentWorkers(t *testing.T) {
	p := newIsolatedWukongStore(t)
	ctx := t.Context()
	aggregateID := fmt.Sprintf("stale-lease-%d", time.Now().UnixNano())
	payload, _ := json.Marshal(map[string]any{"user_id": "user-a", "friend_id": "user-b"})
	var outboxID int64
	if err := p.pool.QueryRow(ctx, `
		INSERT INTO im_wukong_outbox(idempotency_key,operation,aggregate_type,aggregate_id,payload,status,attempts,locked_at)
		VALUES($1,$2,'fault_test',$3,$4,'processing',3,now()-interval '2 minutes') RETURNING id
	`, "lease:"+aggregateID, wukong.OperationFriendAllow, aggregateID, payload).Scan(&outboxID); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan []wukong.OutboxItem, 2)
	errors := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			items, err := p.ClaimWukongOutbox(ctx, 1)
			results <- items
			errors <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	claimed := []wukong.OutboxItem{}
	for items := range results {
		claimed = append(claimed, items...)
	}
	if len(claimed) != 1 || claimed[0].ID != outboxID || claimed[0].Attempts != 4 {
		t.Fatalf("claimed=%+v", claimed)
	}
	if err := p.FailWukongOutbox(ctx, outboxID, "injected timeout", true); err != nil {
		t.Fatal(err)
	}
	var status, lastError string
	var lockedAt *time.Time
	if err := p.pool.QueryRow(ctx, `SELECT status,last_error,locked_at FROM im_wukong_outbox WHERE id=$1`, outboxID).Scan(&status, &lastError, &lockedAt); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || lastError != "injected timeout" || lockedAt != nil {
		t.Fatalf("status=%s lastError=%q lockedAt=%v", status, lastError, lockedAt)
	}
}

func newIsolatedWukongStore(t *testing.T) *Postgres {
	t.Helper()
	rawURL := os.Getenv("IM_TEST_DATABASE_URL")
	if rawURL == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := t.Context()
	bootstrap, err := pgxpool.New(ctx, rawURL)
	if err != nil {
		t.Fatal(err)
	}
	database := fmt.Sprintf("wk_fault_%d", time.Now().UnixNano())
	if _, err = bootstrap.Exec(ctx, `CREATE DATABASE `+database); err != nil {
		bootstrap.Close()
		t.Fatal(err)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		bootstrap.Close()
		t.Fatal(err)
	}
	parsed.Path = "/" + database
	query := parsed.Query()
	query.Del("search_path")
	parsed.RawQuery = query.Encode()
	p, err := NewPostgres(ctx, parsed.String())
	if err != nil {
		_, _ = bootstrap.Exec(context.Background(), `DROP DATABASE `+database+` WITH (FORCE)`)
		bootstrap.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = bootstrap.Exec(cleanupCtx, `DROP DATABASE `+database+` WITH (FORCE)`)
		bootstrap.Close()
	})
	return p
}

func TestRuntimeStatsExposePermanentWukongFailures(t *testing.T) {
	p := newIsolatedWukongStore(t)
	ctx := t.Context()
	if _, err := p.pool.Exec(ctx, `
		INSERT INTO im_wukong_outbox(idempotency_key,operation,aggregate_type,aggregate_id,payload,status,last_error)
		VALUES('stats-failed-outbox','message.store','test','stats','{}','failed','sanitized failure');
		INSERT INTO im_wukong_webhook_events(id,event_type,payload,status,last_error,received_at)
		VALUES('stats-failed-webhook','msg.notify','{}','failed','sanitized failure',now());
	`); err != nil {
		t.Fatal(err)
	}
	stats, err := p.RuntimeStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.WukongOutboxFailed != 1 || stats.WukongWebhookFailed != 1 {
		t.Fatalf("permanent failure gauges outbox=%d webhook=%d", stats.WukongOutboxFailed, stats.WukongWebhookFailed)
	}
}

func TestAdminTaskStatusExposesWukongQueuesAndReconciliation(t *testing.T) {
	p := newIsolatedWukongStore(t)
	ctx := t.Context()
	if _, err := p.pool.Exec(ctx, `
		INSERT INTO im_wukong_outbox(idempotency_key,operation,aggregate_type,aggregate_id,payload,status,created_at)
		VALUES('admin-reconcile-pending','channel.reconcile','channel','2:g1','{}','pending',now()-interval '30 seconds');
		INSERT INTO im_wukong_outbox(idempotency_key,operation,aggregate_type,aggregate_id,payload,status,completed_at)
		VALUES('admin-reconcile-completed','channel.reconcile','channel','2:g2','{}','completed',now());
		INSERT INTO im_wukong_outbox(idempotency_key,operation,aggregate_type,aggregate_id,payload,status,last_error)
		VALUES('admin-reconcile-failed','channel.reconcile','channel','2:g3','{}','failed','sanitized failure');
		INSERT INTO im_wukong_webhook_events(id,event_type,payload,status,received_at)
		VALUES('admin-webhook-processing','msg.notify','{}','processing',now()-interval '20 seconds');
		INSERT INTO im_wukong_webhook_events(id,event_type,payload,status,last_error,received_at)
		VALUES('admin-webhook-failed','msg.offline','{}','failed','sanitized failure',now());
	`); err != nil {
		t.Fatal(err)
	}
	status, err := p.AdminTaskStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	outbox := status["wukongOutbox"].(map[string]any)
	webhook := status["wukongWebhook"].(map[string]any)
	if outbox["pending"] != int64(1) || outbox["failed"] != int64(1) || outbox["reconcilePending"] != int64(1) || outbox["reconcileCompleted"] != int64(1) || outbox["reconcileFailed"] != int64(1) || outbox["lastCompletedAt"] == nil || outbox["oldestPendingSeconds"].(float64) < 29 {
		t.Fatalf("outbox status=%+v", outbox)
	}
	if webhook["processing"] != int64(1) || webhook["failed"] != int64(1) || webhook["oldestPendingSeconds"].(float64) < 19 {
		t.Fatalf("webhook status=%+v", webhook)
	}
}

func waitForWukongOutboxState(t *testing.T, p *Postgres, id int64, wantStatus string, wantAttempts int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		var attempts int
		if err := p.pool.QueryRow(t.Context(), `SELECT status,attempts FROM im_wukong_outbox WHERE id=$1`, id).Scan(&status, &attempts); err != nil {
			t.Fatal(err)
		}
		if status == wantStatus && attempts == wantAttempts {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("outbox %d did not reach %s/%d", id, wantStatus, wantAttempts)
}
