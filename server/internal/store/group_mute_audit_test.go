package store

import (
	"testing"
	"time"
)

func TestGroupMuteAllEventDataPreservesBeforeAndAfter(t *testing.T) {
	at := time.Date(2026, 9, 15, 8, 0, 0, 0, time.UTC)
	before := at.Add(time.Hour)
	data := groupMuteAllEventData(&before, nil, at, "  incident recovery  ")
	if data["muted"] != false || data["reason"] != "incident recovery" {
		t.Fatalf("top-level data=%#v", data)
	}
	beforeState, ok := data["before"].(map[string]any)
	if !ok || beforeState["muted"] != true || beforeState["until"] != before.Format(time.RFC3339Nano) {
		t.Fatalf("before=%#v", data["before"])
	}
	afterState, ok := data["after"].(map[string]any)
	if !ok || afterState["muted"] != false || afterState["until"] != nil {
		t.Fatalf("after=%#v", data["after"])
	}
}

func TestSameOptionalTime(t *testing.T) {
	now := time.Now().UTC()
	same := now
	later := now.Add(time.Second)
	if !sameOptionalTime(nil, nil) || !sameOptionalTime(&now, &same) || sameOptionalTime(&now, nil) || sameOptionalTime(&now, &later) {
		t.Fatal("optional time equality is incorrect")
	}
}
