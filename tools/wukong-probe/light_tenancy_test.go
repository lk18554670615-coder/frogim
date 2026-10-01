package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Opt-in only; every account and conversation is synthetic in the local A/B stack.
func TestLightTenancyIMCallAndIdentity(t *testing.T) {
	if os.Getenv("LIGHT_TENANCY_LOCAL") != "1" {
		t.Skip("set LIGHT_TENANCY_LOCAL=1 with the isolated local stack running")
	}
	p := "http://127.0.0.1:18700/platform"
	req := func(method, address, token string, body any) (int, map[string]any) {
		b, _ := json.Marshal(body)
		r, _ := http.NewRequest(method, address, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Client-Platform", "web")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		client := http.Client{Timeout: 20 * time.Second}
		res, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		if data, ok := out["data"].(map[string]any); ok {
			out = data
		}
		return res.StatusCode, out
	}
	okreq := func(method, address, token string, body any) map[string]any {
		code, out := req(method, address, token, body)
		if code < 200 || code >= 300 {
			t.Fatalf("%s %s status %d", method, strings.Split(address, "?")[0], code)
		}
		return out
	}
	str := func(m map[string]any, key string) string { s, _ := m[key].(string); return s }
	obj := func(m map[string]any, key string) map[string]any { v, _ := m[key].(map[string]any); return v }
	admin := str(okreq("POST", p+"/admin/auth/login", "", map[string]string{"username": "admin", "password": "LocalAdmin123!"}), "token")
	type session struct{ uid, phone, access, directory, im string }
	login := func(phone, password string) session {
		g := okreq("POST", p+"/v2/auth/password-login", "", map[string]string{"phone": phone, "password": password})
		grant := obj(g, "enterprise")
		api := str(obj(obj(grant, "tenant"), "services"), "apiBaseUrl")
		s := okreq("POST", api+"/v2/auth/enterprise-session", "", map[string]string{"ticket": str(grant, "ticket")})
		return session{str(obj(s, "user"), "id"), phone, str(s, "accessToken"), str(g, "accessToken"), str(obj(s, "imSession"), "token")}
	}
	phone := fmt.Sprintf("139%08d", time.Now().UnixNano()%100000000)
	peerPhone := fmt.Sprintf("137%08d", time.Now().UnixNano()%100000000)
	for _, number := range []string{phone, peerPhone} {
		okreq("POST", p+"/v2/auth/register", "", map[string]string{"phone": number, "password": "LocalUser123!", "name": "本机通信验收", "code": "123456", "inviteCode": "A"})
	}
	a, b := login(phone, "LocalUser123!"), login(peerPhone, "LocalUser123!")
	api := "http://127.0.0.1:18701"
	f := okreq("POST", api+"/v2/contacts/requests", a.access, map[string]string{"userID": b.uid, "message": "本机测试"})
	okreq("POST", api+"/v2/contacts/requests/"+str(f, "id")+"/accept", b.access, map[string]string{})
	conversation := str(okreq("POST", api+"/v2/channels/direct", a.access, map[string]string{"userID": b.uid}), "id")
	conn, err := connectRawDevice(context.Background(), "tcp://127.0.0.1:18740", a.uid, a.im, wkproto.WEB)
	if err != nil {
		t.Fatal("IM handshake:", err)
	}
	defer conn.Close()
	peer, err := connectRawDevice(context.Background(), "tcp://127.0.0.1:18740", b.uid, b.im, wkproto.WEB)
	if err != nil {
		t.Fatal("peer IM handshake:", err)
	}
	defer peer.Close()
	message := okreq("POST", api+"/v2/messages/conversations/"+conversation+"/send", a.access, map[string]any{"clientMsgID": "local-" + phone, "type": "text", "body": map[string]string{"text": "轻量企业直连验收"}})
	id, err := strconv.ParseInt(str(obj(message, "message"), "id"), 10, 64)
	if err != nil || id <= 0 {
		t.Fatal("invalid delivered message identity")
	}
	if _, err = waitRawMessage(peer, id, 5*time.Second); err != nil {
		t.Fatal("IM delivery:", err)
	}
	// Verify persisted history independently of the business message's ID representation.
	history := okreq("GET", api+"/v2/messages/conversations/"+conversation+"/history", b.access, nil)
	if !strings.Contains(fmt.Sprint(history), "轻量企业直连验收") {
		t.Fatal("message missing from enterprise history")
	}
	t.Log("direct enterprise message and history passed")
	if os.Getenv("LIGHT_TENANCY_OUTAGE") == "1" {
		func() {
			if err := exec.Command("docker", "stop", "frogim-light-platform-1").Run(); err != nil {
				t.Fatal("stop local platform:", err)
			}
			defer func() {
				if err := exec.Command("docker", "start", "frogim-light-platform-1").Run(); err != nil {
					t.Error("restore local platform:", err)
					return
				}
				until := time.Now().Add(20 * time.Second)
				for time.Now().Before(until) {
					res, err := (&http.Client{Timeout: time.Second}).Get("http://127.0.0.1:18700/ready")
					if err == nil {
						res.Body.Close()
						if res.StatusCode == 200 {
							return
						}
					}
					time.Sleep(200 * time.Millisecond)
				}
				t.Error("restored platform not ready")
			}()
			profile := okreq("GET", api+"/v2/users/me", a.access, nil)
			if str(profile, "id") != a.uid {
				t.Fatal("valid enterprise session changed during platform outage")
			}
			outageMessage := okreq("POST", api+"/v2/messages/conversations/"+conversation+"/send", a.access, map[string]any{"clientMsgID": "outage-" + phone, "type": "text", "body": map[string]string{"text": "平台离线仍直连企业"}})
			id, err := strconv.ParseInt(str(obj(outageMessage, "message"), "id"), 10, 64)
			if err != nil {
				t.Fatal("outage message identity")
			}
			if _, err = waitRawMessage(peer, id, 5*time.Second); err != nil {
				t.Fatal("enterprise message during outage:", err)
			}
			for _, path := range []string{"/v2/auth/password-login", "/v2/auth/refresh"} {
				body := bytes.NewBufferString(`{"phone":"` + phone + `","password":"LocalUser123!","refreshToken":"unavailable"}`)
				r, _ := http.NewRequest("POST", p+path, body)
				r.Header.Set("Content-Type", "application/json")
				if response, err := (&http.Client{Timeout: time.Second}).Do(r); err == nil {
					response.Body.Close()
					t.Fatalf("platform outage request %s unexpectedly available", path)
				}
			}
			code, _ := req("POST", api+"/v2/auth/password-login", "", map[string]string{"phone": phone, "password": "LocalUser123!"})
			if code != 409 {
				t.Fatal("enterprise allowed independent login during outage")
			}
			t.Log("platform stopped: valid enterprise session and actual IM delivery continue; new login/renewal cannot bypass platform")
		}()
	}
	call := obj(okreq("POST", api+"/v2/calls/invite", a.access, map[string]string{"conversationID": conversation, "calleeUserID": b.uid, "callID": "local-" + phone, "mediaType": "audio"}), "call")
	callID := str(call, "id")
	okreq("POST", api+"/v2/calls/"+callID+"/accept", b.access, map[string]string{})
	media := obj(okreq("POST", api+"/v2/calls/"+callID+"/token", a.access, map[string]string{}), "session")
	u, _ := url.Parse(str(media, "url"))
	u.Scheme = "http"
	u.Path += "/rtc"
	q := u.Query()
	q.Set("access_token", str(media, "token"))
	q.Set("protocol", "16")
	q.Set("sdk", "js")
	q.Set("version", "2.18.0")
	u.RawQuery = q.Encode()
	ws, err := net.DialTimeout("tcp", u.Host, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.SetDeadline(time.Now().Add(5 * time.Second))
	r, _ := http.NewRequest("GET", u.String(), nil)
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", "MDEyMzQ1Njc4OWFiY2RlZg==")
	_ = r.Write(ws)
	reader := bufio.NewReader(ws)
	response, err := http.ReadResponse(reader, r)
	if err != nil || response.StatusCode != 101 {
		if err != nil {
			t.Fatal("RTC handshake:", err)
		}
		t.Fatalf("RTC handshake status %d", response.StatusCode)
	}
	_, err = reader.Peek(2)
	if err != nil {
		t.Fatal("RTC join response missing:", err)
	}
	t.Log("existing enterprise API LiveKit WebSocket handshake passed (no microphone/media acceptance claimed)")
	switchTo := func(target string) {
		user := obj(okreq("GET", p+"/admin/users/"+a.uid, admin, nil), "user")
		out := okreq("POST", p+"/admin/users/"+a.uid+"/switch", admin, map[string]any{"tenantId": target, "version": user["assignmentVersion"], "reason": "本机通信撤权测试", "confirmed": true})
		if out["phase"] != "done" {
			t.Fatal("switch incomplete")
		}
	}
	switchTo("enterprise-b")
	if _, err = waitRawDisconnect(conn, 4*time.Second); err != nil {
		if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
			t.Fatal("old IM remained connected")
		}
	}
	if _, err = connectRawDevice(context.Background(), "tcp://127.0.0.1:18740", a.uid, a.im, wkproto.WEB); err == nil {
		t.Fatal("old IM credential reconnected")
	}
	u.Path += "/validate"
	code, _ := req("GET", u.String(), "", nil)
	if code != 403 {
		t.Fatalf("old RTC token status %d", code)
	}
	code, _ = req("GET", api+"/v2/users/me", a.access, nil)
	if code != 401 && code != 403 {
		t.Fatalf("old business token status %d", code)
	}
	_ = ws.SetReadDeadline(time.Now().Add(4 * time.Second))
	for {
		_, err = reader.ReadByte()
		if err != nil {
			break
		}
	}
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("old RTC stream remained open")
	}
	t.Log("old IM, business and RTC reconnect credentials rejected; RTC stream closed")
	switchTo("enterprise-a")
	a = login(phone, "LocalUser123!")
	history = okreq("GET", api+"/v2/messages/conversations/"+conversation+"/history", a.access, nil)
	if !strings.Contains(fmt.Sprint(history), "轻量企业直连验收") {
		t.Fatal("A history lost on return")
	}
	newPhone := fmt.Sprintf("136%08d", time.Now().UnixNano()%100000000)
	okreq("PATCH", p+"/v2/users/me/phone", a.directory, map[string]string{"phone": newPhone, "code": "123456"})
	code, _ = req("POST", p+"/v2/auth/password-login", "", map[string]string{"phone": phone, "password": "LocalUser123!"})
	if code != 401 {
		t.Fatal("old phone still authenticates")
	}
	a = login(newPhone, "LocalUser123!")
	if a.uid == "" {
		t.Fatal("new phone login failed")
	}
	user := obj(okreq("GET", p+"/admin/users/"+a.uid, admin, nil), "user")
	okreq("POST", p+"/admin/users/"+a.uid+"/reset-password", admin, map[string]any{"password": "LocalNewPassword123!", "version": user["assignmentVersion"], "reason": "本机重置测试", "confirmed": true})
	code, _ = req("POST", p+"/v2/auth/password-login", "", map[string]string{"phone": newPhone, "password": "LocalUser123!"})
	if code != 401 {
		t.Fatal("old password still authenticates")
	}
	_ = login(newPhone, "LocalNewPassword123!")
	t.Log("phone uniqueness coordination and password reset passed")
}
