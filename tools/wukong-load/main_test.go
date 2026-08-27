package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSummarizeUsesNearestRankPercentiles(t *testing.T) {
	values := make([]time.Duration, 100)
	for index := range values {
		values[index] = time.Duration(index+1) * time.Millisecond
	}
	report := summarize(values)
	if report.MinimumMS != 1 || report.P50MS != 50 || report.P95MS != 95 || report.P99MS != 99 || report.MaximumMS != 100 || report.MeanMS != 50.5 {
		t.Fatalf("report=%+v", report)
	}
}

func TestValidateRejectsUnsafeOrMeaninglessRuns(t *testing.T) {
	valid := options{
		managerToken: "secret", connections: 2, connectionWorkers: 1,
		connectionAttempts: 3, connectionRetryDelay: 100 * time.Millisecond,
		messagePairs:      1,
		messagesPerSecond: 1, duration: time.Second, ackWait: time.Second,
		maxP95: 300 * time.Millisecond, maxP99: 800 * time.Millisecond, minimumACKRatio: 1,
	}
	if err := validate(valid); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.connections = 1
	if err := validate(invalid); err == nil {
		t.Fatal("single-connection run was accepted")
	}
	invalid = valid
	invalid.maxP99 = 100 * time.Millisecond
	if err := validate(invalid); err == nil {
		t.Fatal("P99 below P95 was accepted")
	}
	invalid = valid
	invalid.minimumACKRatio = 1.01
	if err := validate(invalid); err == nil {
		t.Fatal("ACK ratio above one was accepted")
	}
	invalid = valid
	invalid.connectionAttempts = 0
	if err := validate(invalid); err == nil {
		t.Fatal("zero connection attempts were accepted")
	}
	invalid = valid
	invalid.connectionRetryDelay = -time.Millisecond
	if err := validate(invalid); err == nil {
		t.Fatal("negative connection retry delay was accepted")
	}
	invalid = valid
	invalid.messagePairs = 2
	if err := validate(invalid); err == nil {
		t.Fatal("message pairs without two clients each were accepted")
	}
	invalid = valid
	invalid.requirePolicy = true
	if err := validate(invalid); err == nil || !strings.Contains(err.Error(), "business-api") {
		t.Fatalf("missing business policy inputs were accepted: %v", err)
	}
	valid.requirePolicy = true
	valid.businessURL = "http://business.test"
	valid.otpCode = "123456"
	if err := validate(valid); err != nil {
		t.Fatalf("valid business policy run rejected: %v", err)
	}
	valid.messagePairs = 6
	valid.connections = 12
	if err := validate(valid); err == nil || !strings.Contains(err.Error(), "internal fixture") {
		t.Fatalf("public auth was allowed to bypass login rate limits: %v", err)
	}
	valid.provisionSecret = strings.Repeat("p", 32)
	if err := validate(valid); err != nil {
		t.Fatalf("internal fixture run rejected: %v", err)
	}
}

func TestBusinessPairPhonesAreStableDistinctAndElevenDigits(t *testing.T) {
	aSender, aReceiver := businessPairPhones("run-a", 0)
	bSender, bReceiver := businessPairPhones("run-a", 1)
	againSender, againReceiver := businessPairPhones("run-a", 0)
	if aSender != againSender || aReceiver != againReceiver {
		t.Fatalf("phone allocation is not stable: %s %s / %s %s", aSender, aReceiver, againSender, againReceiver)
	}
	for _, phone := range []string{aSender, aReceiver, bSender, bReceiver} {
		if len(phone) != 11 {
			t.Fatalf("phone %q does not contain 11 digits", phone)
		}
	}
	if aSender == aReceiver || aSender == bSender || aReceiver == bReceiver {
		t.Fatalf("phone allocation collided: %q %q %q %q", aSender, aReceiver, bSender, bReceiver)
	}
}

func TestWaitForConnectionRetryHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitForConnectionRetry(ctx, time.Second, 1) {
		t.Fatal("cancelled retry wait returned true")
	}
	if !waitForConnectionRetry(context.Background(), 0, 1) {
		t.Fatal("zero-delay retry was rejected")
	}
}

func TestProvisionUserUsesPinnedManagerContractWithoutLeakingToken(t *testing.T) {
	managerToken := "manager-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/token" || r.Header.Get("token") != managerToken {
			http.Error(w, "invalid manager request", http.StatusUnauthorized)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["uid"] != "load-user" || body["token"] != "load-token" || body["device_flag"] != float64(0) || body["device_level"] != float64(1) {
			t.Fatalf("body=%v", body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	cfg := options{apiURL: server.URL, managerToken: managerToken}
	if err := provisionUser(context.Background(), server.Client(), cfg, "load-user", "load-token"); err != nil {
		t.Fatal(err)
	}
}

func TestProvisionBusinessPairCreatesPolicyFactsAndUsesIssuedIMSessions(t *testing.T) {
	var mu sync.Mutex
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v2/auth/login":
			if r.Header.Get("X-Client-Platform") != "android" {
				t.Fatalf("platform=%q", r.Header.Get("X-Client-Platform"))
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["code"] != "654321" {
				t.Fatalf("login body=%v", body)
			}
			uid := "usr_sender"
			if strings.HasPrefix(body["phone"].(string), "138") {
				uid = "usr_receiver"
			}
			json.NewEncoder(w).Encode(map[string]any{
				"accessToken": "access_" + uid,
				"user":        map[string]any{"id": uid},
				"imSession":   map[string]any{"uid": uid, "token": "im_" + uid},
			})
		case r.URL.Path == "/v2/contacts/requests":
			if r.Header.Get("Authorization") != "Bearer access_usr_sender" {
				t.Fatalf("friend auth=%q", r.Header.Get("Authorization"))
			}
			json.NewEncoder(w).Encode(map[string]any{"id": "req_1"})
		case r.URL.Path == "/v2/contacts/requests/req_1/accept":
			if r.Header.Get("Authorization") != "Bearer access_usr_receiver" {
				t.Fatalf("accept auth=%q", r.Header.Get("Authorization"))
			}
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/v2/channels/direct":
			if r.Header.Get("Authorization") != "Bearer access_usr_sender" {
				t.Fatalf("direct auth=%q", r.Header.Get("Authorization"))
			}
			json.NewEncoder(w).Encode(map[string]any{"id": "conv_1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	uids := []string{"old_sender", "old_receiver"}
	tokens := []string{"old_sender_token", "old_receiver_token"}
	cfg := options{businessURL: server.URL, otpCode: "654321"}
	if err := provisionBusinessPair(context.Background(), cfg, uids, tokens); err != nil {
		t.Fatal(err)
	}
	if uids[0] != "usr_sender" || uids[1] != "usr_receiver" || tokens[0] != "im_usr_sender" || tokens[1] != "im_usr_receiver" {
		t.Fatalf("credentials=%v %v", uids, tokens)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 5 || requests[2] != "/v2/contacts/requests" || requests[4] != "/v2/channels/direct" {
		t.Fatalf("requests=%v", requests)
	}
}

func TestProvisionBusinessPairsInternalUsesSecretAndCompleteSessions(t *testing.T) {
	secret := strings.Repeat("p", 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/wukong/load/pairs" || r.Header.Get("X-IM-Wukong-Policy-Secret") != secret {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var request struct {
			RunID string `json:"runId"`
			Pairs int    `json:"pairs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.RunID != "fixture-run" || request.Pairs != 2 {
			t.Fatalf("request=%+v", request)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"runId": request.RunID,
			"pairs": []any{
				map[string]any{"sender": map[string]any{"uid": "s0", "token": "st0"}, "receiver": map[string]any{"uid": "r0", "token": "rt0"}},
				map[string]any{"sender": map[string]any{"uid": "s1", "token": "st1"}, "receiver": map[string]any{"uid": "r1", "token": "rt1"}},
			},
		})
	}))
	defer server.Close()
	uids := []string{"old0", "old1", "old2", "old3"}
	tokens := []string{"ot0", "ot1", "ot2", "ot3"}
	cfg := options{businessURL: server.URL, provisionSecret: secret, runID: "fixture-run"}
	if err := provisionBusinessPairs(context.Background(), cfg, uids, tokens, 2); err != nil {
		t.Fatal(err)
	}
	if strings.Join(uids, ",") != "s0,r0,s1,r1" || strings.Join(tokens, ",") != "st0,rt0,st1,rt1" {
		t.Fatalf("credentials=%v %v", uids, tokens)
	}
}
