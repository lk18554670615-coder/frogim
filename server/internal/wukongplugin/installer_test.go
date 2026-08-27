package wukongplugin

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallerRequiresAllowlistedSignedExactBundle(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bundle := []byte("linux executable test payload")
	digest := sha256.Sum256(bundle)
	manifest := Manifest{SchemaVersion: 1, PluginNo: "wk.plugin.safe", Name: "wk.plugin.safe-linux-amd64.wkp", Version: "1.2.3", Methods: []string{"Send", "Route"}, OS: "linux", Arch: "amd64", FileName: "wk.plugin.safe-linux-amd64.wkp", SHA256: hex.EncodeToString(digest[:]), Size: int64(len(bundle)), KeyID: "release-key"}
	manifestJSON, _ := json.Marshal(manifest)
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, manifestJSON))
	directory := t.TempDir()
	installer, err := New(Config{Directory: directory, TrustedKeys: map[string]ed25519.PublicKey{"release-key": publicKey}, Allowlist: map[string]struct{}{"wk.plugin.safe": {}}, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	installation, err := installer.Stage(manifestJSON, signature, bytes.NewReader(bundle), false)
	if err != nil {
		t.Fatal(err)
	}
	if err = installation.Commit(); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(filepath.Join(directory, manifest.FileName))
	if err != nil || !bytes.Equal(written, bundle) {
		t.Fatalf("written=%q err=%v", written, err)
	}

	if _, err = installer.Stage(manifestJSON, signature, bytes.NewReader([]byte("tampered")), true); !errors.Is(err, ErrBundleMismatch) {
		t.Fatalf("tampered bundle err=%v", err)
	}
	manifest.PluginNo = "wk.plugin.not-allowed"
	manifest.FileName = "wk.plugin.not-allowed-linux-amd64.wkp"
	manifest.Name = manifest.FileName
	manifestJSON, _ = json.Marshal(manifest)
	signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, manifestJSON))
	if _, err = installer.Stage(manifestJSON, signature, bytes.NewReader(bundle), false); !errors.Is(err, ErrPluginNotAllowed) {
		t.Fatalf("not allowlisted err=%v", err)
	}
}

func TestInstallerRejectsUnsignedAndAIPlugin(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	bundle := []byte("payload")
	digest := sha256.Sum256(bundle)
	manifest := Manifest{SchemaVersion: 1, PluginNo: "wk.plugin.safe", Name: "wk.plugin.safe-linux-amd64.wkp", Version: "1", Methods: []string{"Receive"}, OS: "linux", Arch: "amd64", FileName: "wk.plugin.safe-linux-amd64.wkp", SHA256: hex.EncodeToString(digest[:]), Size: int64(len(bundle)), KeyID: "release-key"}
	manifestJSON, _ := json.Marshal(manifest)
	installer, err := New(Config{Directory: t.TempDir(), TrustedKeys: map[string]ed25519.PublicKey{"release-key": publicKey}, Allowlist: map[string]struct{}{"wk.plugin.safe": {}}, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = installer.Stage(manifestJSON, "", bytes.NewReader(bundle), false); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("unsigned err=%v", err)
	}
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, manifestJSON))
	if _, err = installer.Stage(manifestJSON, signature, bytes.NewReader(bundle), false); !errors.Is(err, ErrAIPluginForbidden) {
		t.Fatalf("AI err=%v", err)
	}
}

func TestInstallerUpgradeRollbackAndLifecycleMoves(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	directory := t.TempDir()
	installer, err := New(Config{Directory: directory, TrustedKeys: map[string]ed25519.PublicKey{"release-key": publicKey}, Allowlist: map[string]struct{}{"wk.plugin.safe": {}}, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	stage := func(version string, body []byte, replace bool) *Installation {
		t.Helper()
		digest := sha256.Sum256(body)
		manifest := Manifest{SchemaVersion: 1, PluginNo: "wk.plugin.safe", Name: "wk.plugin.safe-linux-amd64.wkp", Version: version, Methods: []string{"Send"}, OS: "linux", Arch: "amd64", FileName: "wk.plugin.safe-linux-amd64.wkp", SHA256: hex.EncodeToString(digest[:]), Size: int64(len(body)), KeyID: "release-key"}
		raw, _ := json.Marshal(manifest)
		installation, stageErr := installer.Stage(raw, base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, raw)), bytes.NewReader(body), replace)
		if stageErr != nil {
			t.Fatal(stageErr)
		}
		return installation
	}
	first := stage("1", []byte("first"), false)
	if err = first.Commit(); err != nil {
		t.Fatal(err)
	}
	second := stage("2", []byte("second"), true)
	if err = second.Rollback(); err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(directory, "wk.plugin.safe-linux-amd64.wkp")
	if body, _ := os.ReadFile(active); string(body) != "first" {
		t.Fatalf("rollback body=%q", body)
	}
	if err = installer.Disable(filepath.Base(active)); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(active + ".disabled"); err != nil {
		t.Fatal(err)
	}
	if err = installer.Enable(filepath.Base(active)); err != nil {
		t.Fatal(err)
	}
	if err = installer.Remove(filepath.Base(active)); err != nil {
		t.Fatal(err)
	}
	if err = installer.Remove(filepath.Base(active)); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("second remove err=%v", err)
	}
}
