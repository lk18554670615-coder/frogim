// Offline, operator-only encrypted backup delivery. It does not provision cloud
// storage, print credentials, delete remote objects or activate restored data.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/linli/im/server/internal/backup"
	"github.com/linli/im/server/internal/privatefile"
)

type configuration struct {
	Destination      backup.OffsiteConfig `json:"destination"`
	Expected         backup.Binding       `json:"expected"`
	ArchiveDirectory string               `json:"archiveDirectory"`
	JournalDirectory string               `json:"journalDirectory"`
	DeliveryFile     string               `json:"deliveryFile,omitempty"`
	Actor            string               `json:"actor"`
	Reason           string               `json:"reason"`
	DailyDate        string               `json:"dailyDate,omitempty"`
}
type intent struct {
	Version         int            `json:"version"`
	Mode            string         `json:"mode"`
	Actor           string         `json:"actor"`
	Reason          string         `json:"reason"`
	DestinationHash string         `json:"destinationHash"`
	ArchivePathHash string         `json:"archivePathHash"`
	Binding         backup.Binding `json:"binding"`
	Manifest        backup.File    `json:"manifest"`
}

func readPrivate(path string, limit int64) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, backup.ErrInvalid
	}
	i, e := os.Lstat(path)
	if e != nil || !i.Mode().IsRegular() || i.Size() > limit {
		return nil, backup.ErrInvalid
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, backup.ErrInvalid
	}
	defer f.Close()
	a, e := f.Stat()
	if e != nil || !os.SameFile(i, a) {
		return nil, backup.ErrInvalid
	}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(b)) > limit {
		return nil, backup.ErrInvalid
	}
	return b, nil
}
func decodeFile(path string, value any) error {
	raw, e := readPrivate(path, 1<<20)
	if e != nil {
		return e
	}
	defer clear(raw)
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(value) != nil || d.Decode(new(any)) != io.EOF {
		return backup.ErrInvalid
	}
	return nil
}
func resolved(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", backup.ErrInvalid
	}
	parent, e := filepath.EvalSymlinks(filepath.Dir(path))
	if e != nil {
		return "", backup.ErrInvalid
	}
	path = filepath.Join(parent, filepath.Base(path))
	if i, e := os.Lstat(path); e == nil && i.Mode()&os.ModeSymlink != 0 {
		return "", backup.ErrInvalid
	}
	return path, nil
}
func outside(parent, path string) bool {
	if !strings.EqualFold(filepath.VolumeName(parent), filepath.VolumeName(path)) {
		return true
	}
	rel, e := filepath.Rel(parent, path)
	return e == nil && (rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
func saveOnce(path string, value any) error {
	raw, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return backup.ErrInvalid
	}
	if old, e := readPrivate(path, 1<<20); e == nil {
		if bytes.Equal(old, raw) {
			return nil
		}
		return backup.ErrInvalid
	}
	f, e := privatefile.Create(path)
	if e != nil {
		return backup.ErrInvalid
	}
	defer f.Close()
	if n, e := f.Write(raw); e != nil || n != len(raw) || f.Sync() != nil {
		return backup.ErrOffsite
	}
	return nil
}

type stampedRecord struct {
	RecordedAt time.Time       `json:"recordedAt"`
	Data       json.RawMessage `json:"data"`
}

func saveStamped(path string, value any) error {
	raw, e := json.Marshal(value)
	if e != nil {
		return backup.ErrInvalid
	}
	if existing, e := readPrivate(path, 1<<20); e == nil {
		var prior stampedRecord
		d := json.NewDecoder(bytes.NewReader(existing))
		d.DisallowUnknownFields()
		var compact bytes.Buffer
		if d.Decode(&prior) != nil || d.Decode(new(any)) != io.EOF || prior.RecordedAt.IsZero() || json.Compact(&compact, prior.Data) != nil || !bytes.Equal(compact.Bytes(), raw) {
			return backup.ErrInvalid
		}
		return nil
	}
	return saveOnce(path, stampedRecord{RecordedAt: time.Now().UTC(), Data: raw})
}
func hash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func execute(ctx context.Context, mode, configPath, keyPath string, confirmed bool) (string, error) {
	if mode != "policy" && mode != "upload" && mode != "download" && mode != "discover-daily" {
		return "", backup.ErrInvalid
	}
	if mode != "policy" && !confirmed {
		return "", backup.ErrInvalid
	}
	var c configuration
	if e := decodeFile(configPath, &c); e != nil {
		return "", e
	}
	if mode == "policy" {
		if keyPath != "" {
			return "", backup.ErrInvalid
		}
		policy, e := c.Destination.IAMPolicy()
		return string(policy), e
	}
	if len(strings.TrimSpace(c.Actor)) == 0 || len(c.Actor) > 100 || len(strings.TrimSpace(c.Reason)) < 3 || len(c.Reason) > 500 {
		return "", backup.ErrInvalid
	}
	if mode == "discover-daily" {
		return discoverDaily(ctx, c, keyPath)
	}
	if c.DailyDate != "" {
		return "", backup.ErrInvalid
	}
	archive, e := resolved(c.ArchiveDirectory)
	if e != nil {
		return "", e
	}
	journal, e := resolved(c.JournalDirectory)
	if e != nil {
		return "", e
	}
	keyPath, e = resolved(keyPath)
	if e != nil {
		return "", e
	}
	if !outside(archive, keyPath) || !outside(archive, journal) || !outside(journal, archive) || !outside(journal, keyPath) {
		return "", backup.ErrInvalid
	}
	key, e := readPrivate(keyPath, 32)
	if e != nil || len(key) != 32 {
		return "", backup.ErrInvalid
	}
	defer clear(key)
	o, e := backup.NewOffsite(c.Destination)
	if e != nil {
		return "", e
	}
	defer o.Close()
	if !o.Accepts(c.Expected) {
		return "", backup.ErrInvalid
	}
	var delivery backup.Delivery
	if mode == "upload" {
		if c.DeliveryFile != "" {
			return "", backup.ErrInvalid
		}
		r, e := backup.Open(archive, c.Expected, key)
		if e != nil {
			return "", e
		}
		delivery = backup.Delivery{Version: 1, TargetID: c.Destination.ID, Binding: c.Expected, Manifest: r.ManifestProof()}
		r.Close()
	} else if decodeFile(c.DeliveryFile, &delivery) != nil || delivery.Binding != c.Expected || delivery.TargetID != c.Destination.ID {
		return "", backup.ErrInvalid
	}
	public := c.Destination
	public.AccessKey = ""
	public.SecretKey = ""
	public.CAFile = ""
	encoded, _ := json.Marshal(public)
	request := intent{1, mode, c.Actor, c.Reason, hash(encoded), hash([]byte(archive)), c.Expected, delivery.Manifest}
	if e = os.Mkdir(journal, 0700); e != nil && !os.IsExist(e) {
		return "", backup.ErrInvalid
	}
	if e = saveStamped(filepath.Join(journal, "intent.json"), request); e != nil {
		return "", e
	}
	if mode == "upload" {
		actual, e := o.Deliver(ctx, c.Expected, archive, key)
		if e != nil || actual != delivery {
			return "", backup.ErrOffsite
		}
	} else if e = o.Retrieve(ctx, c.Expected, delivery, archive, key); e != nil {
		return "", e
	}
	if e = saveStamped(filepath.Join(journal, "completed.json"), struct {
		Intent   intent          `json:"intent"`
		Delivery backup.Delivery `json:"delivery"`
	}{request, delivery}); e != nil {
		return "", e
	}
	if e = saveOnce(filepath.Join(journal, "delivery.json"), delivery); e != nil {
		return "", e
	}
	return "Encrypted backup " + mode + " verified; immutable delivery receipt saved. No key/plaintext sent, no original archive overwritten, no restore activated.", nil
}
func main() {
	mode := flag.String("mode", "", "policy, upload, download or discover-daily")
	config := flag.String("config", "", "absolute private operator configuration")
	key := flag.String("key-file", "", "independent 32-byte key outside archive and journal")
	confirmed := flag.Bool("confirmed", false, "operator confirms this isolated ciphertext transfer")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 12*time.Hour)
	defer cancel()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "offsite request rejected")
		os.Exit(2)
	}
	message, e := execute(ctx, *mode, *config, *key, *confirmed)
	if e != nil {
		fmt.Fprintln(os.Stderr, "Offsite backup unconfirmed or rejected. Retain journal and partial artifacts; no automatic overwrite, remote deletion or activation.")
		os.Exit(1)
	}
	fmt.Println(message)
}
