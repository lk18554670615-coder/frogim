package wukong

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// OnlineUsers follows pinned v2.2.5 user.go: a raw UID array in, one row
// per connected device out. Missing users are offline only on a valid response.
func (c *Client) OnlineUsers(ctx context.Context, ids []string) (map[string]bool, error) {
	if len(ids) == 0 || len(ids) > 200 {
		return nil, errors.New("presence requires 1–200 users")
	}
	var raw json.RawMessage
	if err := c.post(ctx, c.apiURL, "/user/onlinestatus", ids, &raw, true); err != nil {
		return nil, err
	}
	var rows []struct {
		UID        string `json:"uid"`
		Online     *int   `json:"online"`
		DeviceFlag int    `json:"device_flag"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil || rows == nil {
		return nil, errors.New("invalid WuKongIM presence response")
	}
	result := make(map[string]bool, len(ids))
	for _, id := range ids {
		result[id] = false
	}
	for _, row := range rows {
		if row.UID == "" || row.Online == nil || (*row.Online != 0 && *row.Online != 1) {
			return nil, errors.New("invalid WuKongIM presence row")
		}
		if _, requested := result[row.UID]; requested && *row.Online == 1 {
			result[row.UID] = true
		}
	}
	return result, nil
}

type Presence struct {
	UserID    string    `json:"userId"`
	Status    string    `json:"status"`
	CheckedAt time.Time `json:"checkedAt"`
}
type presenceFlight struct {
	done  chan struct{}
	value Presence
}
type presenceEntry struct {
	value   Presence
	expires time.Time
}

// Authorization is NEVER cached. Flights are shared by UID, including
// overlapping batches. Both memory and upstream concurrency are bounded.
type PresenceCache struct {
	mu      sync.Mutex
	entries map[string]presenceEntry
	flights map[string]*presenceFlight
	load    func(context.Context, []string) (map[string]bool, error)
	slots   chan struct{}
	now     func() time.Time
}

func NewPresenceCache(load func(context.Context, []string) (map[string]bool, error)) *PresenceCache {
	return &PresenceCache{entries: map[string]presenceEntry{}, flights: map[string]*presenceFlight{}, load: load, slots: make(chan struct{}, 2), now: time.Now}
}
func (p *PresenceCache) Query(ctx context.Context, ids []string) map[string]Presence {
	result := make(map[string]Presence, len(ids))
	waiting := map[string]*presenceFlight{}
	var missing []string
	p.mu.Lock()
	now := p.now().UTC()
	for id, entry := range p.entries {
		if !now.Before(entry.expires) {
			delete(p.entries, id)
		}
	}
	for _, id := range ids {
		if entry, ok := p.entries[id]; ok {
			result[id] = entry.value
			continue
		}
		flight := p.flights[id]
		if flight == nil {
			if len(p.flights) >= 10000 {
				result[id] = Presence{id, "unknown", now}
				continue
			}
			flight = &presenceFlight{done: make(chan struct{})}
			p.flights[id] = flight
			missing = append(missing, id)
		}
		waiting[id] = flight
	}
	p.mu.Unlock()
	for start := 0; start < len(missing); start += 200 {
		go p.fetch(missing[start:min(start+200, len(missing))])
	}
	for id, flight := range waiting {
		select {
		case <-ctx.Done():
			result[id] = Presence{id, "unknown", p.now().UTC()}
		case <-flight.done:
			result[id] = flight.value
		}
	}
	return result
}
func (p *PresenceCache) fetch(ids []string) {
	// Independent of the first caller's cancellation, but never unbounded.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var values map[string]bool
	err := errors.New("presence unavailable")
	select {
	case p.slots <- struct{}{}:
		if p.load != nil {
			values, err = p.load(ctx, ids)
		}
		<-p.slots
	case <-ctx.Done():
		err = ctx.Err()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now().UTC()
	for _, id := range ids {
		value := Presence{id, "unknown", now}
		if err == nil {
			if online, ok := values[id]; ok {
				value.Status = "offline"
				if online {
					value.Status = "online"
				}
			}
		}
		delete(p.entries, id)
		if value.Status != "unknown" {
			if len(p.entries) >= 10000 {
				for key := range p.entries {
					delete(p.entries, key)
					break
				}
			}
			p.entries[id] = presenceEntry{value, now.Add(5 * time.Second)}
		}
		flight := p.flights[id]
		flight.value = value
		delete(p.flights, id)
		close(flight.done)
	}
}
