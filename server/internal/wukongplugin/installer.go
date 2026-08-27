package wukongplugin

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	ManifestSchemaVersion = 1
	DefaultMaxBundleBytes = int64(64 << 20)
)

var (
	ErrUnavailable       = errors.New("WuKongIM plugin installer is unavailable")
	ErrInvalidManifest   = errors.New("invalid WuKongIM plugin manifest")
	ErrUntrustedSigner   = errors.New("untrusted WuKongIM plugin signer")
	ErrPluginNotAllowed  = errors.New("WuKongIM plugin is not allowlisted")
	ErrInvalidSignature  = errors.New("invalid WuKongIM plugin signature")
	ErrBundleMismatch    = errors.New("WuKongIM plugin bundle does not match its manifest")
	ErrAIPluginForbidden = errors.New("AI WuKongIM plugins are disabled")
	ErrAlreadyInstalled  = errors.New("WuKongIM plugin is already installed")
	ErrNotInstalled      = errors.New("WuKongIM plugin is not installed")
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,127}$`)
var fileStemPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,239}$`)

// Manifest is signed verbatim as the detached Ed25519 message. The signed
// bytes, rather than re-encoded JSON, are verified so publishers can reproduce
// the signature without depending on Go's JSON encoder.
type Manifest struct {
	SchemaVersion int      `json:"schemaVersion"`
	PluginNo      string   `json:"pluginNo"`
	Name          string   `json:"name"`
	Version       string   `json:"version"`
	Methods       []string `json:"methods"`
	OS            string   `json:"os"`
	Arch          string   `json:"arch"`
	FileName      string   `json:"fileName"`
	SHA256        string   `json:"sha256"`
	Size          int64    `json:"size"`
	KeyID         string   `json:"keyId"`
}

type Config struct {
	Directory   string
	TrustedKeys map[string]ed25519.PublicKey
	Allowlist   map[string]struct{}
	MaxBytes    int64
}

type Installer struct {
	directory string
	keys      map[string]ed25519.PublicKey
	allowlist map[string]struct{}
	maxBytes  int64
}

type Installation struct {
	Manifest     Manifest
	ManifestJSON []byte
	TargetPath   string
	backupPath   string
	committed    bool
}

func New(config Config) (*Installer, error) {
	directory := strings.TrimSpace(config.Directory)
	if directory == "" || len(config.TrustedKeys) == 0 || len(config.Allowlist) == 0 {
		return nil, ErrUnavailable
	}
	if config.MaxBytes == 0 {
		config.MaxBytes = DefaultMaxBundleBytes
	}
	if config.MaxBytes < 1<<20 || config.MaxBytes > 512<<20 {
		return nil, fmt.Errorf("%w: maximum bundle size is outside the supported range", ErrInvalidManifest)
	}
	if err := ensureDirectory(directory); err != nil {
		return nil, err
	}
	keys := make(map[string]ed25519.PublicKey, len(config.TrustedKeys))
	for keyID, publicKey := range config.TrustedKeys {
		keyID = strings.TrimSpace(keyID)
		if !identifierPattern.MatchString(keyID) || len(publicKey) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%w: invalid public key %q", ErrInvalidManifest, keyID)
		}
		keys[keyID] = append(ed25519.PublicKey(nil), publicKey...)
	}
	allowlist := make(map[string]struct{}, len(config.Allowlist))
	for pluginNo := range config.Allowlist {
		pluginNo = strings.TrimSpace(pluginNo)
		if !identifierPattern.MatchString(pluginNo) {
			return nil, fmt.Errorf("%w: invalid allowlist entry %q", ErrInvalidManifest, pluginNo)
		}
		allowlist[pluginNo] = struct{}{}
	}
	return &Installer{directory: directory, keys: keys, allowlist: allowlist, maxBytes: config.MaxBytes}, nil
}

func ParseTrustedKeys(raw string) (map[string]ed25519.PublicKey, error) {
	keys := map[string]ed25519.PublicKey{}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		parts := strings.SplitN(item, ":", 2)
		if len(parts) != 2 || !identifierPattern.MatchString(strings.TrimSpace(parts[0])) {
			return nil, fmt.Errorf("%w: trusted keys must use key-id:base64-public-key", ErrInvalidManifest)
		}
		decoded, err := decodeBase64(strings.TrimSpace(parts[1]))
		if err != nil || len(decoded) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%w: public key %q is not Ed25519", ErrInvalidManifest, parts[0])
		}
		keyID := strings.TrimSpace(parts[0])
		if _, exists := keys[keyID]; exists {
			return nil, fmt.Errorf("%w: duplicate public key %q", ErrInvalidManifest, keyID)
		}
		keys[keyID] = ed25519.PublicKey(decoded)
	}
	return keys, nil
}

func ParseAllowlist(raw string) (map[string]struct{}, error) {
	allowlist := map[string]struct{}{}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !identifierPattern.MatchString(item) {
			return nil, fmt.Errorf("%w: invalid plugin number %q", ErrInvalidManifest, item)
		}
		allowlist[item] = struct{}{}
	}
	return allowlist, nil
}

func (i *Installer) VerifyManifest(manifestJSON []byte, detachedSignature string) (Manifest, error) {
	if i == nil {
		return Manifest{}, ErrUnavailable
	}
	return i.verifyManifest(manifestJSON, detachedSignature)
}

func (i *Installer) Stage(manifestJSON []byte, detachedSignature string, bundle io.Reader, replace bool) (*Installation, error) {
	if i == nil {
		return nil, ErrUnavailable
	}
	manifest, err := i.verifyManifest(manifestJSON, detachedSignature)
	if err != nil {
		return nil, err
	}
	if bundle == nil {
		return nil, fmt.Errorf("%w: bundle is required", ErrBundleMismatch)
	}
	if err = ensureDirectory(i.directory); err != nil {
		return nil, err
	}
	temporary, err := os.CreateTemp(i.directory, ".wkp-upload-*")
	if err != nil {
		return nil, err
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		_ = temporary.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, hasher), io.LimitReader(bundle, i.maxBytes+1))
	if closeErr := temporary.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return nil, copyErr
	}
	if written > i.maxBytes || written != manifest.Size || hex.EncodeToString(hasher.Sum(nil)) != manifest.SHA256 {
		return nil, ErrBundleMismatch
	}
	if err = os.Chmod(temporaryPath, 0550); err != nil {
		return nil, err
	}

	target := filepath.Join(i.directory, manifest.FileName)
	if err = safeRegularOrMissing(target); err != nil {
		return nil, err
	}
	installation := &Installation{Manifest: manifest, ManifestJSON: append([]byte(nil), manifestJSON...), TargetPath: target}
	if _, statErr := os.Lstat(target); statErr == nil {
		if !replace {
			return nil, ErrAlreadyInstalled
		}
		installation.backupPath = target + ".rollback-" + fmt.Sprint(time.Now().UnixNano())
		if err = os.Rename(target, installation.backupPath); err != nil {
			return nil, err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, statErr
	}
	if err = os.Rename(temporaryPath, target); err != nil {
		if installation.backupPath != "" {
			_ = os.Rename(installation.backupPath, target)
		}
		return nil, err
	}
	removeTemporary = false
	return installation, nil
}

func (i *Installer) Disable(fileName string) error {
	active, disabled, err := i.lifecyclePaths(fileName)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(disabled); err == nil {
		return nil
	}
	if err = safeRegularOrMissing(active); err != nil {
		return err
	}
	if _, err = os.Lstat(active); errors.Is(err, os.ErrNotExist) {
		return ErrNotInstalled
	} else if err != nil {
		return err
	}
	return os.Rename(active, disabled)
}

func (i *Installer) Enable(fileName string) error {
	active, disabled, err := i.lifecyclePaths(fileName)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(active); err == nil {
		return nil
	}
	if err = safeRegularOrMissing(disabled); err != nil {
		return err
	}
	if _, err = os.Lstat(disabled); errors.Is(err, os.ErrNotExist) {
		return ErrNotInstalled
	} else if err != nil {
		return err
	}
	return os.Rename(disabled, active)
}

func (i *Installer) Remove(fileName string) error {
	active, disabled, err := i.lifecyclePaths(fileName)
	if err != nil {
		return err
	}
	found := false
	for _, candidate := range []string{active, disabled} {
		if err = safeRegularOrMissing(candidate); err != nil {
			return err
		}
		if err = os.Remove(candidate); err == nil {
			found = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if !found {
		return ErrNotInstalled
	}
	return nil
}

func (i *Installer) verifyManifest(raw []byte, signatureText string) (Manifest, error) {
	var manifest Manifest
	if len(raw) == 0 || len(raw) > 64<<10 {
		return manifest, ErrInvalidManifest
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return manifest, ErrInvalidManifest
	}
	manifest.PluginNo = strings.TrimSpace(manifest.PluginNo)
	manifest.Name = strings.TrimSpace(manifest.Name)
	manifest.Version = strings.TrimSpace(manifest.Version)
	manifest.KeyID = strings.TrimSpace(manifest.KeyID)
	manifest.SHA256 = strings.ToLower(strings.TrimSpace(manifest.SHA256))
	if manifest.SchemaVersion != ManifestSchemaVersion || !identifierPattern.MatchString(manifest.PluginNo) || manifest.Name == "" || len(manifest.Name) > 128 || manifest.Version == "" || len(manifest.Version) > 64 || !identifierPattern.MatchString(manifest.KeyID) {
		return manifest, ErrInvalidManifest
	}
	if manifest.OS != "linux" || manifest.Arch != "amd64" || filepath.Base(manifest.FileName) != manifest.FileName || filepath.Ext(manifest.FileName) != ".wkp" || !fileStemPattern.MatchString(strings.TrimSuffix(manifest.FileName, ".wkp")) || !strings.HasPrefix(manifest.FileName, manifest.PluginNo+"-") {
		return manifest, ErrInvalidManifest
	}
	// go-pdk v1.0.3 reports path.Base(os.Executable()) as PluginInfo.Name.
	// Requiring the same value makes post-start attestation deterministic.
	if manifest.Name != manifest.FileName {
		return manifest, ErrInvalidManifest
	}
	if manifest.Size <= 0 || manifest.Size > i.maxBytes {
		return manifest, ErrInvalidManifest
	}
	digest, err := hex.DecodeString(manifest.SHA256)
	if err != nil || len(digest) != sha256.Size {
		return manifest, ErrInvalidManifest
	}
	if _, allowed := i.allowlist[manifest.PluginNo]; !allowed {
		return manifest, ErrPluginNotAllowed
	}
	publicKey, trusted := i.keys[manifest.KeyID]
	if !trusted {
		return manifest, ErrUntrustedSigner
	}
	signature, err := decodeBase64(strings.TrimSpace(signatureText))
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(publicKey, raw, signature) {
		return manifest, ErrInvalidSignature
	}
	allowedMethods := map[string]bool{"Send": true, "PersistAfter": true, "Route": true, "ConfigUpdate": true, "Receive": true}
	seen := map[string]bool{}
	for _, method := range manifest.Methods {
		if !allowedMethods[method] || seen[method] {
			return manifest, ErrInvalidManifest
		}
		if method == "Receive" {
			return manifest, ErrAIPluginForbidden
		}
		seen[method] = true
	}
	sort.Strings(manifest.Methods)
	return manifest, nil
}

func (i *Installer) lifecyclePaths(fileName string) (string, string, error) {
	if i == nil {
		return "", "", ErrUnavailable
	}
	if filepath.Base(fileName) != fileName || filepath.Ext(fileName) != ".wkp" || !fileStemPattern.MatchString(strings.TrimSuffix(fileName, ".wkp")) {
		return "", "", ErrInvalidManifest
	}
	active := filepath.Join(i.directory, fileName)
	return active, active + ".disabled", nil
}

func (i *Installation) Commit() error {
	if i == nil || i.committed {
		return nil
	}
	if i.backupPath != "" {
		if err := os.Remove(i.backupPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	i.committed = true
	return nil
}

func (i *Installation) Rollback() error {
	if i == nil || i.committed {
		return nil
	}
	if err := os.Remove(i.TargetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if i.backupPath != "" {
		if err := os.Rename(i.backupPath, i.TargetPath); err != nil {
			return err
		}
	}
	i.committed = true
	return nil
}

func ensureDirectory(directory string) error {
	if err := os.MkdirAll(directory, 0750); err != nil {
		return err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("plugin directory must be a real directory")
	}
	return nil
}

func safeRegularOrMissing(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("plugin target must be a regular file")
	}
	return nil
}

func decodeBase64(value string) ([]byte, error) {
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if decoded, err := encoding.DecodeString(value); err == nil {
			return decoded, nil
		}
	}
	return nil, errors.New("invalid base64")
}
