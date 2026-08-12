package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestPostgresPersistsSignedPluginReleaseAndLifecycleEvents(t *testing.T) {
	databaseURL := os.Getenv("IM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	repository, err := NewPostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	suffix := fmt.Sprint(time.Now().UnixNano())
	pluginNo := "wk.plugin.test-" + suffix
	fileName := pluginNo + "-linux-amd64.wkp"
	defer func() {
		_, _ = repository.pool.Exec(ctx, `DELETE FROM im_wukong_plugin_events WHERE plugin_no=$1`, pluginNo)
		_, _ = repository.pool.Exec(ctx, `DELETE FROM im_wukong_plugin_releases WHERE plugin_no=$1`, pluginNo)
	}()
	now := time.Now().UTC().Truncate(time.Microsecond)
	release, err := repository.SaveWukongPluginRelease(ctx, WukongPluginRelease{
		PluginNo: pluginNo, NodeID: 1, Name: fileName, FileName: fileName, Version: "1.2.3",
		Methods: []string{"Route", "Send"}, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SizeBytes: 4096, KeyID: "release-key", Status: "active", Manifest: map[string]any{"schemaVersion": float64(1)},
		LastActor: "admin-test", LastReason: "integration test", InstalledAt: &now,
	})
	if err != nil || release.PluginNo != pluginNo || release.Status != "active" || len(release.Methods) != 2 {
		t.Fatalf("save release=%#v err=%v", release, err)
	}
	loaded, err := repository.GetWukongPluginRelease(ctx, pluginNo)
	if err != nil || loaded.FileName != fileName || loaded.KeyID != "release-key" {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	if err = repository.RecordWukongPluginEvent(ctx, WukongPluginEvent{PluginNo: pluginNo, Action: "install", Status: "active", Actor: "admin-test", Reason: "integration test", Details: map[string]any{"version": "1.2.3"}, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	events, err := repository.ListWukongPluginEvents(ctx, pluginNo, 10)
	if err != nil || len(events) != 1 || events[0].Action != "install" || events[0].Details["version"] != "1.2.3" {
		t.Fatalf("events=%#v err=%v", events, err)
	}
	release.Status, release.LastReason = "disabled", "maintenance"
	updated, err := repository.SaveWukongPluginRelease(ctx, *release)
	if err != nil || updated.Status != "disabled" || updated.InstalledAt == nil {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	items, err := repository.ListWukongPluginReleases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		found = found || item.PluginNo == pluginNo
	}
	if !found {
		t.Fatalf("plugin %s missing from releases", pluginNo)
	}
}
