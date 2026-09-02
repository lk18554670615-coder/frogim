package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"net/http"
	"os"
	"testing"
	"time"
)

// Real connections against the pinned isolated server; no mock online flags.
func TestPinnedPresenceMultipleDevices(t *testing.T) {
	api, tcp := os.Getenv("IM_TEST_WUKONG_PATCH_URL"), os.Getenv("IM_TEST_WUKONG_TCP_URL")
	if api == "" || tcp == "" {
		t.Skip("isolated pinned WuKongIM required")
	}
	cfg := options{apiURL: api, tcpURL: tcp, managerToken: os.Getenv("IM_TEST_WUKONG_MANAGER_TOKEN")}
	uid := fmt.Sprintf("presence_%d", time.Now().UnixNano())
	token := "local_presence_token"
	for _, flag := range []wkproto.DeviceFlag{wkproto.APP, wkproto.WEB} {
		if err := postJSON(t.Context(), cfg, "/user/token", map[string]any{"uid": uid, "token": token, "device_flag": flag, "device_level": 1}); err != nil {
			t.Fatal(err)
		}
	}
	query := func() int {
		t.Helper()
		body, _ := json.Marshal([]string{uid, uid + "_offline"})
		req, _ := http.NewRequestWithContext(t.Context(), "POST", api+"/user/onlinestatus", bytes.NewReader(body))
		req.Header.Set("token", cfg.managerToken)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatal(res.StatusCode)
		}
		var rows []struct {
			UID        string `json:"uid"`
			Online     int    `json:"online"`
			DeviceFlag int    `json:"device_flag"`
		}
		if err = json.NewDecoder(res.Body).Decode(&rows); err != nil {
			t.Fatal(err)
		}
		if rows == nil {
			t.Fatal("expected an array, not null")
		}
		for _, r := range rows {
			if r.UID != uid || r.Online != 1 {
				t.Fatalf("unexpected row=%+v", r)
			}
		}
		return len(rows)
	}
	expect := func(want int) {
		t.Helper()
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			if query() == want {
				return
			}
			time.Sleep(40 * time.Millisecond)
		}
		t.Fatalf("online device rows=%d want=%d", query(), want)
	}
	expect(0)
	app, err := connectRawDevice(t.Context(), tcp, uid, token, wkproto.APP)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	expect(1)
	web, err := connectRawDevice(t.Context(), tcp, uid, token, wkproto.WEB)
	if err != nil {
		t.Fatal(err)
	}
	defer web.Close()
	expect(2)
	if err = app.Close(); err != nil {
		t.Fatal(err)
	}
	expect(1)
	if err = web.Close(); err != nil {
		t.Fatal(err)
	}
	expect(0)
	t.Log("offline -> APP online -> APP+WEB online -> APP exits, WEB stays online -> all offline")
}
