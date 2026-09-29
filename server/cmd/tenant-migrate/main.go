// tenant-migrate is the operator receipt gate around the existing cold-copy,
// explicit enterprise adoption and tenant-import commands. It never runs
// arbitrary shell text, touches original volumes or silently enables business.
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
	"path/filepath"
	"time"

	"github.com/linli/im/server/internal/legacyimport"
)

type configuration struct {
	Request             legacyimport.CutoverRequest `json:"request"`
	Step                legacyimport.CutoverStep    `json:"step"`
	PlatformDatabaseURL string                      `json:"platformDatabaseUrl"`
	AdoptedDatabaseURL  string                      `json:"adoptedDatabaseUrl"`
	EvidenceFile        string                      `json:"evidenceFile"`
}
type evidence struct {
	Phase            string `json:"phase"`
	OriginalDatabase string `json:"originalDatabase"`
	AdoptedDatabase  string `json:"adoptedDatabase"`
	OldStackStopped  bool   `json:"oldStackStopped"`
	OldAuthIsolated  bool   `json:"oldAuthIsolated"`
	NewStackPrivate  bool   `json:"newStackPrivate"`
	// Explicit verification records, not inferred from successful SQL import.
	VerificationSHA256 string      `json:"verificationSha256"`
	Files              []proofFile `json:"files"`
}
type proofFile struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func readJSON(path string, value any) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, legacyimport.ErrConfig
	}
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, legacyimport.ErrConfig
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		return nil, legacyimport.ErrConfig
	}
	// Retain the exact file hash; unknown fields are almost always operator typos.
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(value) != nil || d.Decode(new(any)) != io.EOF {
		return nil, legacyimport.ErrConfig
	}
	return raw, nil
}
func digest(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func validDigest(v string) bool {
	b, e := hex.DecodeString(v)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == v
}
func verifyEvidence(c configuration) error {
	var e evidence
	raw, err := readJSON(c.EvidenceFile, &e)
	if err != nil || digest(raw) != c.Step.EvidenceSHA256 || e.Phase != c.Step.Phase || e.OriginalDatabase != c.Request.OriginalDatabase || e.AdoptedDatabase != c.Request.AdoptedDatabase || !e.OldStackStopped || !e.OldAuthIsolated || !validDigest(e.VerificationSHA256) {
		return legacyimport.ErrConfig
	}
	if e.Phase != "completed" && !e.NewStackPrivate {
		return legacyimport.ErrMaintenance
	}
	kinds := map[string]bool{}
	for _, p := range e.Files {
		if !filepath.IsAbs(p.Path) || kinds[p.Kind] || p.Size <= 0 || !validDigest(p.SHA256) {
			return legacyimport.ErrConfig
		}
		info, err := os.Lstat(p.Path)
		if err != nil || !info.Mode().IsRegular() || info.Size() != p.Size {
			return legacyimport.ErrConfig
		}
		f, err := os.Open(p.Path)
		if err != nil {
			return legacyimport.ErrRead
		}
		h := sha256.New()
		n, err := io.Copy(h, f)
		f.Close()
		if err != nil || n != p.Size || hex.EncodeToString(h.Sum(nil)) != p.SHA256 {
			return legacyimport.ErrRead
		}
		kinds[p.Kind] = true
	}
	if e.Phase == "backed_up" {
		for _, kind := range []string{"database", "media", "im", "redis", "deployment"} {
			if !kinds[kind] {
				return legacyimport.ErrConfig
			}
		}
	}
	return nil
}
func execute(ctx context.Context, mode, path string, confirmed bool) (legacyimport.CutoverStatus, error) {
	var out legacyimport.CutoverStatus
	var c configuration
	if _, e := readJSON(path, &c); e != nil {
		return out, e
	}
	if mode != "status" && !confirmed {
		return out, legacyimport.ErrConfig
	}
	open := legacyimport.OpenLocalForImport
	if mode == "status" {
		open = legacyimport.OpenLocalReadOnly
	}
	p, e := open(ctx, c.PlatformDatabaseURL)
	if e != nil {
		return out, e
	}
	defer p.Close()
	switch mode {
	case "start":
		return legacyimport.StartCutover(ctx, p, c.Request, confirmed)
	case "status":
		return legacyimport.ReadCutover(ctx, p, c.Request)
	case "advance":
		if e = verifyEvidence(c); e != nil {
			return out, e
		}
		if c.Step.Phase == "stopped" || c.Step.Phase == "backed_up" || c.Step.Phase == "rolled_back" || c.Step.Phase == "completed" {
			return legacyimport.AdvanceCutover(ctx, nil, p, c.Request, c.Step)
		}
		s, e := legacyimport.OpenLocalForImport(ctx, c.AdoptedDatabaseURL)
		if e != nil {
			return out, e
		}
		defer s.Close()
		return legacyimport.AdvanceCutover(ctx, s, p, c.Request, c.Step)
	default:
		return out, legacyimport.ErrConfig
	}
}
func main() {
	mode := flag.String("mode", "status", "start, status, advance")
	config := flag.String("config", "", "absolute private JSON configuration")
	confirmed := flag.Bool("confirmed", false, "explicit maintenance action")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, e := execute(ctx, *mode, *config, *confirmed)
	if e != nil {
		fmt.Fprintln(os.Stderr, "LEGACY_CUTOVER_UNCONFIRMED: verify configuration, evidence and maintenance state; no automatic rollback")
		os.Exit(2)
	}
	_ = json.NewEncoder(os.Stdout).Encode(out)
}
