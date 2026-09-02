package push

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/linli/im/server/internal/store"
)

type dispatcherStore struct {
	items       []store.OutboxItem
	invalidated []string
	completed   error
}

func (s *dispatcherStore) ClaimPush(context.Context, int) ([]store.OutboxItem, error) {
	items := s.items
	s.items = nil
	return items, nil
}
func (s *dispatcherStore) CompletePush(_ context.Context, _ int64, err error) error {
	s.completed = err
	return nil
}
func (s *dispatcherStore) InvalidatePushDevices(_ context.Context, ids []string) error {
	s.invalidated = append([]string(nil), ids...)
	return nil
}

type staticProvider struct{ err error }

type presentationStore struct {
	dispatcherStore
	allowed     bool
	policyError error
}

func (s *presentationStore) CanPresentPush(context.Context, store.OutboxItem) (bool, error) {
	return s.allowed, s.policyError
}

type countingProvider struct{ sends int }

func (p *countingProvider) Send(context.Context, store.OutboxItem) error { p.sends++; return nil }
func TestDispatcherChecksCurrentPresentationPolicyBeforeSending(t *testing.T) {
	for _, tc := range []struct {
		allowed bool
		err     error
		sends   int
	}{{false, nil, 0}, {false, errors.New("db unavailable"), 0}, {true, nil, 1}} {
		s := &presentationStore{dispatcherStore: dispatcherStore{items: []store.OutboxItem{{ID: 1, Devices: []store.Device{{ID: "test"}}}}}, allowed: tc.allowed, policyError: tc.err}
		provider := &countingProvider{}
		NewDispatcher(s, provider).once(t.Context())
		if provider.sends != tc.sends || (s.completed != nil) != (tc.err != nil) {
			t.Fatalf("sends=%d completed=%v", provider.sends, s.completed)
		}
	}
}

func (p staticProvider) Send(context.Context, store.OutboxItem) error { return p.err }

func TestDispatcherInvalidatesPermanentDeviceTokenAndCompletesItem(t *testing.T) {
	s := &dispatcherStore{items: []store.OutboxItem{{ID: 1, Devices: []store.Device{{ID: "d1"}}}}}
	d := NewDispatcher(s, staticProvider{err: invalidTokenDeliveryError("d1", "Unregistered")})
	d.once(context.Background())
	if !reflect.DeepEqual(s.invalidated, []string{"d1"}) || s.completed != nil {
		t.Fatalf("invalidated=%v completed=%v", s.invalidated, s.completed)
	}
}

func TestMultiProviderAttemptsEveryChannelAndKeepsRetryableFailure(t *testing.T) {
	invalid := invalidTokenDeliveryError("ios", "BadDeviceToken")
	temporary := retryableDeliveryError(errors.New("temporary Getui failure"))
	err := (MultiProvider{staticProvider{err: temporary}, staticProvider{err: invalid}}).Send(context.Background(), store.OutboxItem{})
	var delivery *DeliveryError
	if !errors.As(err, &delivery) || !delivery.Retryable || delivery.InvalidOnly || !reflect.DeepEqual(delivery.InvalidDeviceIDs, []string{"ios"}) {
		t.Fatalf("combined error=%#v", err)
	}
}
