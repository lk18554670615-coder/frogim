package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestRedisEphemeralCrossInstance(t *testing.T) {
	url := os.Getenv("IM_TEST_REDIS_URL")
	if url == "" {
		t.Skip("IM_TEST_REDIS_URL not set")
	}
	a, err := NewWithRedis(Memory{}, url)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := NewWithRedis(Memory{}, url)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got := make(chan ephemeralEvent, 1)
	ready := make(chan struct{})
	go func() {
		sub := b.redis.Subscribe(ctx, "im:ephemeral")
		defer sub.Close()
		if _, e := sub.Receive(ctx); e != nil {
			return
		}
		close(ready)
		for {
			msg, e := sub.ReceiveMessage(ctx)
			if e != nil {
				return
			}
			var event ephemeralEvent
			if json.Unmarshal([]byte(msg.Payload), &event) == nil && event.Origin != b.id {
				got <- event
				return
			}
		}
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("subscriber not ready")
	}
	if err = a.PublishEphemeral(ctx, []string{"u2"}, "typing", map[string]any{"conversationId": "c1"}); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-got:
		if e.Type != "typing" || len(e.Users) != 1 || e.Users[0] != "u2" {
			t.Fatalf("event=%+v", e)
		}
	case <-ctx.Done():
		t.Fatal("event not delivered")
	}
}
