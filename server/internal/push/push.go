package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/linli/im/server/internal/store"
)

type Provider interface {
	Send(context.Context, store.OutboxItem) error
}

// DeliveryError 把永久错误、可重试错误和失效设备分开，避免错误凭据或坏 token 无限重试。
type DeliveryError struct {
	Err              error
	InvalidDeviceIDs []string
	Retryable        bool
	InvalidOnly      bool
}

func (e *DeliveryError) Error() string {
	if e == nil || e.Err == nil {
		return "push delivery failed"
	}
	return e.Err.Error()
}
func (e *DeliveryError) Unwrap() error   { return e.Err }
func (e *DeliveryError) Permanent() bool { return e != nil && !e.Retryable }

func permanentDeliveryError(err error) error { return &DeliveryError{Err: err} }
func retryableDeliveryError(err error) error {
	return &DeliveryError{Err: err, Retryable: true}
}
func invalidTokenDeliveryError(deviceID, reason string) error {
	return &DeliveryError{
		Err:              errors.New("APNs VoIP device token is no longer valid: " + safeAPNSReason(reason)),
		InvalidDeviceIDs: []string{deviceID}, InvalidOnly: true,
	}
}
func invalidGetuiCIDDeliveryError(deviceID string) error {
	return &DeliveryError{
		Err:              errors.New("Getui CID is no longer valid"),
		InvalidDeviceIDs: []string{deviceID}, InvalidOnly: true,
	}
}

// MultiProvider 始终尝试所有通道；一个通道失败不会阻止另一个通道投递。
type MultiProvider []Provider

func (providers MultiProvider) Send(ctx context.Context, item store.OutboxItem) error {
	var joined []error
	var invalid []string
	retryable := false
	invalidOnly := true
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		err := provider.Send(ctx, item)
		if err == nil {
			continue
		}
		joined = append(joined, err)
		var delivery *DeliveryError
		if errors.As(err, &delivery) {
			invalid = append(invalid, delivery.InvalidDeviceIDs...)
			retryable = retryable || delivery.Retryable
			invalidOnly = invalidOnly && delivery.InvalidOnly
		} else {
			retryable = true
			invalidOnly = false
		}
	}
	if len(joined) == 0 {
		return nil
	}
	return &DeliveryError{Err: errors.Join(joined...), InvalidDeviceIDs: uniqueStrings(invalid), Retryable: retryable, InvalidOnly: invalidOnly}
}

type Noop struct{}

func (Noop) Send(context.Context, store.OutboxItem) error { return nil }

type Log struct{}

func (Log) Send(_ context.Context, item store.OutboxItem) error {
	slog.Info("push", "userId", item.UserID, "type", item.EventType, "deviceCount", len(item.Devices))
	return nil
}

// Webhook delegates provider-specific APNs/FCM delivery to a separately
// credentialed push gateway. Device tokens are sent only over HTTPS.
type Webhook struct {
	URL, Token string
	Client     *http.Client
}

func (w Webhook) Send(ctx context.Context, item store.OutboxItem) error {
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+w.Token)
	req.Header.Set("Content-Type", "application/json")
	client := w.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return fmt.Errorf("push gateway status %d: %s", res.StatusCode, body)
	}
	return nil
}

type Dispatcher struct {
	store           store.OutboxStore
	provider        Provider
	interval        time.Duration
	workers         int
	batchSize       int
	deliveryTimeout time.Duration
}

type DispatcherOptions struct {
	Workers, BatchSize        int
	Interval, DeliveryTimeout time.Duration
}

func NewDispatcher(s store.OutboxStore, p Provider) *Dispatcher {
	return NewDispatcherWithOptions(s, p, DispatcherOptions{})
}
func NewDispatcherWithOptions(s store.OutboxStore, p Provider, options DispatcherOptions) *Dispatcher {
	if options.Workers <= 0 {
		options.Workers = 16
	}
	if options.BatchSize <= 0 {
		options.BatchSize = 200
	}
	if options.Interval <= 0 {
		options.Interval = 100 * time.Millisecond
	}
	if options.DeliveryTimeout <= 0 {
		options.DeliveryTimeout = 15 * time.Second
	}
	return &Dispatcher{store: s, provider: p, interval: options.Interval, workers: options.Workers, batchSize: options.BatchSize, deliveryTimeout: options.DeliveryTimeout}
}
func (d *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.once(ctx)
		}
	}
}
func (d *Dispatcher) once(ctx context.Context) {
	items, err := d.store.ClaimPush(ctx, d.batchSize)
	if err != nil {
		slog.Warn("claim push outbox", "error", err)
		return
	}
	jobs := make(chan store.OutboxItem)
	var wg sync.WaitGroup
	workers := min(d.workers, len(items))
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				d.deliver(ctx, item)
			}
		}()
	}
	for _, item := range items {
		select {
		case jobs <- item:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		}
	}
	close(jobs)
	wg.Wait()
}

func (d *Dispatcher) deliver(ctx context.Context, item store.OutboxItem) {
	deliveryCtx, cancel := context.WithTimeout(ctx, d.deliveryTimeout)
	defer cancel()
	var err error
	if len(item.Devices) > 0 {
		err = d.provider.Send(deliveryCtx, item)
	}
	var delivery *DeliveryError
	if errors.As(err, &delivery) && len(delivery.InvalidDeviceIDs) > 0 {
		if invalidator, ok := d.store.(store.PushDeviceInvalidator); ok {
			if invalidErr := invalidator.InvalidatePushDevices(deliveryCtx, delivery.InvalidDeviceIDs); invalidErr != nil {
				err = retryableDeliveryError(errors.Join(err, fmt.Errorf("invalidate push devices: %w", invalidErr)))
			} else if delivery.InvalidOnly {
				err = nil
			}
		}
	}
	completeCtx, completeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer completeCancel()
	if e := d.store.CompletePush(completeCtx, item.ID, err); e != nil {
		slog.Warn("complete push outbox", "error", e)
	}
}
