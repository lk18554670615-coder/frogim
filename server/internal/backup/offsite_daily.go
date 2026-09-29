package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"
)

// DailyReceipt is an encrypted, immutable discovery index. It grants no restore
// authority. A lost host needs only its independently retained directory ID,
// UTC date, offsite credentials and encryption key, never ListBucket access.
type DailyReceipt struct {
	Version  int      `json:"version"`
	Date     string   `json:"date"`
	Delivery Delivery `json:"delivery"`
}

func validDailyDate(date string) bool {
	t, e := time.Parse("2006-01-02", date)
	return e == nil && t.Year() >= 2020 && t.Format("2006-01-02") == date
}
func (o *Offsite) dailyKey(date string) (string, error) {
	if o == nil || o.objects == nil || !o.config.valid() || o.config.Scope != "platform" || !validDailyDate(date) {
		return "", ErrInvalid
	}
	return o.config.objectRoot() + "/daily/" + date + "/receipt.sealed", nil
}
func (o *Offsite) readDaily(ctx context.Context, date string, key []byte) (DailyReceipt, error) {
	var empty DailyReceipt
	path, e := o.dailyKey(date)
	if e != nil || !o.IndependentKey(key) {
		return empty, ErrInvalid
	}
	r, e := o.objects.get(ctx, path)
	if e != nil {
		return empty, e
	}
	defer r.Close()
	cipher, e := io.ReadAll(io.LimitReader(r, (64<<10)+1))
	if e != nil || len(cipher) > 64<<10 {
		return empty, ErrOffsite
	}
	var plain bytes.Buffer
	if Decrypt(&plain, bytes.NewReader(cipher), key) != nil {
		return empty, ErrOffsite
	}
	var result DailyReceipt
	d := json.NewDecoder(&plain)
	d.DisallowUnknownFields()
	if d.Decode(&result) != nil || d.Decode(new(any)) != io.EOF || result.Version != 1 || result.Date != date {
		return empty, ErrOffsite
	}
	canonical, e := o.receipt(result.Delivery.Binding, result.Delivery.Manifest)
	if e != nil || canonical != result.Delivery {
		return empty, ErrOffsite
	}
	return result, nil
}

func (o *Offsite) ReadDailyReceipt(ctx context.Context, date string, key []byte) (DailyReceipt, error) {
	r, e := o.readDaily(ctx, date, key)
	if e != nil {
		return DailyReceipt{}, ErrOffsite
	}
	return r, nil
}

// Called only after Deliver succeeds. Check the remote authenticated manifest
// before publishing. A retry accepts an equal logical receipt, not equal
// ciphertext (encryption uses a fresh nonce). A different receipt is never
// replaced, including after an uncertain conditional PUT acknowledgment.
func (o *Offsite) PublishDailyReceipt(ctx context.Context, date string, delivery Delivery, key []byte) error {
	path, e := o.dailyKey(date)
	if e != nil || !o.IndependentKey(key) {
		return ErrInvalid
	}
	canonical, e := o.receipt(delivery.Binding, delivery.Manifest)
	if e != nil || canonical != delivery {
		return ErrInvalid
	}
	cipher, e := readObject(ctx, o.objects, o.root(delivery)+"/manifest.sealed", delivery.Manifest.Size)
	if e != nil {
		return ErrOffsite
	}
	h := sha256.Sum256(cipher)
	var plain bytes.Buffer
	if hex.EncodeToString(h[:]) != delivery.Manifest.SHA256 || Decrypt(&plain, bytes.NewReader(cipher), key) != nil {
		return ErrOffsite
	}
	var manifest Manifest
	d := json.NewDecoder(&plain)
	d.DisallowUnknownFields()
	if d.Decode(&manifest) != nil || d.Decode(new(any)) != io.EOF || manifest.Version != 1 || manifest.Binding != delivery.Binding {
		return ErrOffsite
	}
	want := DailyReceipt{Version: 1, Date: date, Delivery: delivery}
	got, e := o.readDaily(ctx, date, key)
	if e == nil {
		if got == want {
			return nil
		}
		return ErrOffsite
	}
	if !errors.Is(e, errObjectAbsent) {
		return ErrOffsite
	}
	data, _ := json.Marshal(want)
	var sealed bytes.Buffer
	if Encrypt(&sealed, bytes.NewReader(data), key) != nil {
		return ErrOffsite
	}
	_ = o.objects.putNew(ctx, path, sealed.Bytes())
	got, e = o.readDaily(ctx, date, key)
	if e != nil || got != want {
		return ErrOffsite
	}
	return nil
}

// DestinationFingerprint pins the storage authority without pinning rotatable
// credentials or a local CA pathname. Never use it as an authentication secret.
func (o *Offsite) DestinationFingerprint() string {
	if o == nil || !o.config.valid() {
		return ""
	}
	c := o.config
	c.AccessKey, c.SecretKey, c.CAFile = "", "", ""
	b, _ := json.Marshal(c)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
