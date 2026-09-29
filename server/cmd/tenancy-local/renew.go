package main

import (
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Local development only. Preserve a complete backup before replacing any
// gateway certificate. Do not touch control identities, credentials or DBs.
func renewPublicTLS(root string) error {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	marker, err := os.ReadFile(filepath.Join(absolute, "initialized.json"))
	if err != nil {
		return errors.New("initialize a dedicated local stack first")
	}
	var meta struct {
		Version  int
		TenantID string
	}
	if json.Unmarshal(marker, &meta) != nil || meta.Version != 1 || meta.TenantID != "default" {
		return errors.New("not a recognized local default stack")
	}
	files := []string{"browser-ca.pem", "platform/gateway/ca.pem", "platform/gateway/public.pem", "platform/gateway/public.key", "enterprise/gateway/ca.pem", "enterprise/gateway/public.pem", "enterprise/gateway/public.key"}
	previous := make(map[string][]byte)
	for _, file := range files {
		path := filepath.Join(absolute, filepath.FromSlash(file))
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("local certificate files must be regular files")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		previous[file] = data
	}
	stage, err := os.MkdirTemp(absolute, "public-tls-renewal-")
	if err != nil {
		return err
	}
	for _, file := range files {
		if err = save(stage, "previous/"+file, previous[file]); err != nil {
			return err
		}
	}
	public, err := ca("FrogIM local browser CA")
	if err != nil {
		return err
	}
	for _, dir := range []string{"platform/gateway", "enterprise/gateway"} {
		if err = leaf(filepath.Join(stage, "next"), dir, public, "", []string{"localhost"}, true); err != nil {
			return err
		}
	}
	if err = save(stage, "next/browser-ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: public.der})); err != nil {
		return err
	}
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(stage, "next", filepath.FromSlash(file)))
		if err == nil {
			err = os.WriteFile(filepath.Join(absolute, filepath.FromSlash(file)), data, 0600)
		}
		if err != nil {
			return fmt.Errorf("certificate renewal incomplete; recover previous files from %s", stage)
		}
	}
	if err = jsonFile(stage, "result.json", map[string]any{"completedAt": time.Now().UTC(), "certificateExpiresAt": time.Now().Add(30 * 24 * time.Hour).UTC()}); err != nil {
		return err
	}
	fmt.Println("Local gateway certificates renewed; previous files retained at", filepath.Join(stage, "previous"))
	fmt.Println("Restart only local gateways. No OS trust, account, control certificate or database was modified.")
	return nil
}
