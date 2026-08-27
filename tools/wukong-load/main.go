package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type options struct {
	apiURL               string
	businessURL          string
	tcpURL               string
	managerToken         string
	provisionSecret      string
	otpCode              string
	requirePolicy        bool
	connections          int
	connectionWorkers    int
	connectionAttempts   int
	connectionRetryDelay time.Duration
	messagePairs         int
	messagesPerSecond    float64
	duration             time.Duration
	warmup               time.Duration
	ackWait              time.Duration
	maxP95               time.Duration
	maxP99               time.Duration
	minimumACKRatio      float64
	runID                string
}

type latencyReport struct {
	MinimumMS float64 `json:"minimumMs"`
	P50MS     float64 `json:"p50Ms"`
	P95MS     float64 `json:"p95Ms"`
	P99MS     float64 `json:"p99Ms"`
	MaximumMS float64 `json:"maximumMs"`
	MeanMS    float64 `json:"meanMs"`
}

type loadReport struct {
	ServerCommit              string         `json:"serverCommit"`
	RunID                     string         `json:"runId"`
	BusinessPolicyProvisioned bool           `json:"businessPolicyProvisioned"`
	BusinessProvisionMode     string         `json:"businessProvisionMode,omitempty"`
	MessagePairs              int            `json:"messagePairs"`
	TargetConnections         int            `json:"targetConnections"`
	Connected                 int            `json:"connected"`
	ConnectionFailures        int            `json:"connectionFailures"`
	ConnectionAttempts        int64          `json:"connectionAttempts"`
	ConnectionRetries         int64          `json:"connectionRetries"`
	ConnectionLatency         latencyReport  `json:"connectionLatency"`
	TargetMessagesPerSecond   float64        `json:"targetMessagesPerSecond"`
	TargetMessages            int            `json:"targetMessages"`
	AttemptedMessages         int            `json:"attemptedMessages"`
	AcceptedByClient          int            `json:"acceptedByClient"`
	ClientSendErrors          int            `json:"clientSendErrors"`
	SuccessfulACKs            int            `json:"successfulAcks"`
	RejectedACKs              int            `json:"rejectedAcks"`
	RejectedReasonCodes       map[string]int `json:"rejectedReasonCodes,omitempty"`
	ACKCallbackDrops          int64          `json:"ackCallbackDrops"`
	Unacknowledged            int            `json:"unacknowledged"`
	ReceiverMessages          int64          `json:"receiverMessages"`
	ObservedACKsPerSecond     float64        `json:"observedAcksPerSecond"`
	ACKLatency                latencyReport  `json:"ackLatency"`
	ThresholdP95MS            float64        `json:"thresholdP95Ms"`
	ThresholdP99MS            float64        `json:"thresholdP99Ms"`
	MinimumACKRatio           float64        `json:"minimumAckRatio"`
	ConnectionThresholdPass   bool           `json:"connectionThresholdPass"`
	DeliveryThresholdPass     bool           `json:"deliveryThresholdPass"`
	LatencyThresholdPass      bool           `json:"latencyThresholdPass"`
	ThroughputThresholdPass   bool           `json:"throughputThresholdPass"`
	Passed                    bool           `json:"passed"`
	Elapsed                   string         `json:"elapsed"`
}

type ackRecord struct {
	latency time.Duration
	success bool
	reason  wkproto.ReasonCode
}

type sendResult struct {
	target    int
	attempted int
	accepted  int
	errors    int
	elapsed   time.Duration
}

func main() {
	var cfg options
	flag.StringVar(&cfg.apiURL, "api", envOr("WUKONG_API_URL", "http://127.0.0.1:5001"), "WuKongIM internal HTTP API")
	flag.StringVar(&cfg.businessURL, "business-api", os.Getenv("WUKONG_BUSINESS_API_URL"), "business API used to provision the policy-tested sender and recipient")
	flag.StringVar(&cfg.tcpURL, "tcp", envOr("WUKONG_TCP_URL", "tcp://127.0.0.1:5100"), "WuKongIM TCP endpoint")
	flag.StringVar(&cfg.managerToken, "manager-token", os.Getenv("IM_WUKONG_MANAGER_TOKEN"), "WuKongIM manager token")
	flag.StringVar(&cfg.provisionSecret, "business-provision-secret", os.Getenv("IM_WUKONG_POLICY_SECRET"), "secret for the development-only internal business fixture")
	flag.StringVar(&cfg.otpCode, "otp", os.Getenv("WUKONG_LOAD_OTP"), "development OTP for the disposable load environment")
	flag.BoolVar(&cfg.requirePolicy, "require-business-policy", true, "require PostgreSQL business policy provisioning when messages are sent")
	flag.IntVar(&cfg.connections, "connections", 10000, "number of simultaneous TCP clients")
	flag.IntVar(&cfg.connectionWorkers, "connection-workers", 100, "parallel provision/connect workers")
	flag.IntVar(&cfg.connectionAttempts, "connection-attempts", 3, "maximum handshake attempts per client")
	flag.DurationVar(&cfg.connectionRetryDelay, "connection-retry-delay", 100*time.Millisecond, "base delay between handshake attempts")
	flag.IntVar(&cfg.messagePairs, "message-pairs", 20, "number of independent business sender/receiver pairs used for aggregate message load")
	flag.Float64Var(&cfg.messagesPerSecond, "messages-per-second", 1000, "direct messages sent per second; use zero for a connection-only run")
	flag.DurationVar(&cfg.duration, "duration", time.Minute, "message load duration")
	flag.DurationVar(&cfg.warmup, "warmup", 5*time.Second, "settle time after all handshakes")
	flag.DurationVar(&cfg.ackWait, "ack-wait", 15*time.Second, "maximum drain time for final ACK and receive packets")
	flag.DurationVar(&cfg.maxP95, "max-p95", 300*time.Millisecond, "ACK P95 threshold")
	flag.DurationVar(&cfg.maxP99, "max-p99", 800*time.Millisecond, "ACK P99 threshold")
	flag.Float64Var(&cfg.minimumACKRatio, "minimum-ack-ratio", 0.99, "minimum successful ACK throughput ratio")
	flag.StringVar(&cfg.runID, "run-id", "", "unique run id; generated when omitted")
	flag.Parse()

	report, err := run(cfg)
	encoded, encodeErr := json.MarshalIndent(report, "", "  ")
	if encodeErr == nil {
		fmt.Println(string(encoded))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "load test failed:", err)
		os.Exit(1)
	}
}

func run(cfg options) (loadReport, error) {
	report := loadReport{
		ServerCommit:            "a888f89533d0e7d1b2030e06504ca97f1ad891d4",
		TargetConnections:       cfg.connections,
		TargetMessagesPerSecond: cfg.messagesPerSecond,
		ThresholdP95MS:          milliseconds(cfg.maxP95),
		ThresholdP99MS:          milliseconds(cfg.maxP99),
		MinimumACKRatio:         cfg.minimumACKRatio,
	}
	if cfg.runID == "" {
		cfg.runID = fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	}
	report.RunID = cfg.runID
	if err := validate(cfg); err != nil {
		return report, err
	}
	totalTimeout := 2*cfg.ackWait + cfg.warmup + cfg.duration + 10*time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), totalTimeout)
	defer cancel()
	started := time.Now()
	uids, tokens := loadCredentials(cfg)
	report.MessagePairs = cfg.messagePairs
	if cfg.messagesPerSecond > 0 && strings.TrimSpace(cfg.businessURL) != "" {
		if err := provisionBusinessPairs(ctx, cfg, uids, tokens, cfg.messagePairs); err != nil {
			return report, err
		}
		report.BusinessPolicyProvisioned = true
		if strings.TrimSpace(cfg.provisionSecret) != "" {
			report.BusinessProvisionMode = "internal-dev-fixture"
		} else {
			report.BusinessProvisionMode = "public-auth"
		}
	}
	if err := provisionUsers(ctx, cfg, uids, tokens); err != nil {
		return report, err
	}

	clients := make([]*wkclient.Client, cfg.connections)
	connectionLatencies, connectionFailures, connectionAttempts := connectClients(ctx, cfg, uids, tokens, clients)
	report.Connected = len(connectionLatencies)
	report.ConnectionFailures = connectionFailures
	report.ConnectionAttempts = connectionAttempts
	report.ConnectionRetries = max(0, connectionAttempts-int64(report.Connected))
	report.ConnectionLatency = summarize(connectionLatencies)
	defer closeClients(clients)
	if connectionFailures != 0 || report.Connected != cfg.connections {
		report.Elapsed = time.Since(started).String()
		return report, fmt.Errorf("connected %d/%d clients", report.Connected, cfg.connections)
	}

	if cfg.warmup > 0 {
		select {
		case <-ctx.Done():
			return report, ctx.Err()
		case <-time.After(cfg.warmup):
		}
	}
	report.ConnectionThresholdPass = true
	if cfg.messagesPerSecond == 0 {
		report.DeliveryThresholdPass = true
		report.LatencyThresholdPass = true
		report.ThroughputThresholdPass = true
		report.Passed = true
		report.Elapsed = time.Since(started).String()
		return report, nil
	}

	var receiverMessages atomic.Int64
	pending := make([]sync.Map, cfg.messagePairs)
	ackChannel := make(chan ackRecord, 65536)
	var callbackDrops atomic.Int64
	senders := make([]*wkclient.Client, cfg.messagePairs)
	recipients := make([]string, cfg.messagePairs)
	for pairIndex := range cfg.messagePairs {
		senderIndex, receiverIndex := pairIndex*2, pairIndex*2+1
		senders[pairIndex] = clients[senderIndex]
		recipients[pairIndex] = uids[receiverIndex]
		clients[receiverIndex].SetOnRecv(func(*wkproto.RecvPacket) error {
			receiverMessages.Add(1)
			return nil
		})
		pairPending := &pending[pairIndex]
		clients[senderIndex].SetOnSendack(func(packet *wkproto.SendackPacket) {
			value, ok := pairPending.LoadAndDelete(packet.ClientSeq)
			if !ok {
				return
			}
			record := ackRecord{
				latency: time.Since(value.(time.Time)),
				success: packet.ReasonCode == wkproto.ReasonSuccess && packet.MessageID != 0 && packet.MessageSeq != 0,
				reason:  packet.ReasonCode,
			}
			select {
			case ackChannel <- record:
			default:
				callbackDrops.Add(1)
			}
		})
	}

	sendDone := make(chan sendResult, 1)
	go func() {
		sendDone <- sendMessages(ctx, cfg, senders, recipients, pending)
	}()
	latencies := []time.Duration{}
	rejected := 0
	rejectedReasons := map[string]int{}
	var sent sendResult
	sending := true
	var drainTimer *time.Timer
	var drain <-chan time.Time
	for sending || len(latencies)+rejected < sent.accepted {
		select {
		case record := <-ackChannel:
			if record.success {
				latencies = append(latencies, record.latency)
			} else {
				rejected++
				rejectedReasons[strconv.Itoa(int(record.reason))]++
			}
		case sent = <-sendDone:
			sending = false
			drainTimer = time.NewTimer(cfg.ackWait)
			drain = drainTimer.C
			if len(latencies)+rejected >= sent.accepted {
				drainTimer.Stop()
				drain = nil
			}
		case <-drain:
			sending = false
			goto drained
		case <-ctx.Done():
			return report, ctx.Err()
		}
	}

drained:
	if drainTimer != nil {
		drainTimer.Stop()
	}
	receiveDeadline := time.Now().Add(cfg.ackWait)
	for receiverMessages.Load() < int64(len(latencies)) && time.Now().Before(receiveDeadline) {
		select {
		case <-ctx.Done():
			break
		case <-time.After(10 * time.Millisecond):
		}
	}

	report.TargetMessages = sent.target
	report.AttemptedMessages = sent.attempted
	report.AcceptedByClient = sent.accepted
	report.ClientSendErrors = sent.errors
	report.SuccessfulACKs = len(latencies)
	report.RejectedACKs = rejected
	if len(rejectedReasons) > 0 {
		report.RejectedReasonCodes = rejectedReasons
	}
	report.ACKCallbackDrops = callbackDrops.Load()
	report.Unacknowledged = sent.accepted - len(latencies) - rejected
	if report.Unacknowledged < 0 {
		report.Unacknowledged = 0
	}
	report.ReceiverMessages = receiverMessages.Load()
	report.ACKLatency = summarize(latencies)
	denominator := math.Max(cfg.duration.Seconds(), sent.elapsed.Seconds())
	if denominator > 0 {
		report.ObservedACKsPerSecond = float64(report.SuccessfulACKs) / denominator
	}
	report.DeliveryThresholdPass = report.ClientSendErrors == 0 && report.RejectedACKs == 0 && report.Unacknowledged == 0 && report.ReceiverMessages >= int64(report.SuccessfulACKs)
	report.LatencyThresholdPass = len(latencies) > 0 && report.ACKLatency.P95MS <= milliseconds(cfg.maxP95) && report.ACKLatency.P99MS <= milliseconds(cfg.maxP99)
	report.ThroughputThresholdPass = report.SuccessfulACKs >= int(float64(report.TargetMessages)*cfg.minimumACKRatio) && report.ObservedACKsPerSecond >= cfg.messagesPerSecond*cfg.minimumACKRatio
	report.Passed = report.ConnectionThresholdPass && report.DeliveryThresholdPass && report.LatencyThresholdPass && report.ThroughputThresholdPass
	report.Elapsed = time.Since(started).String()
	if !report.Passed {
		return report, errors.New("one or more load thresholds failed")
	}
	return report, nil
}

func validate(cfg options) error {
	if strings.TrimSpace(cfg.managerToken) == "" {
		return errors.New("manager token is required")
	}
	if cfg.connections < 2 || cfg.connections > 100000 || cfg.connectionWorkers < 1 || cfg.connectionWorkers > 2000 {
		return errors.New("connections must be 2-100000 and workers 1-2000")
	}
	if cfg.connectionAttempts < 1 || cfg.connectionAttempts > 10 || cfg.connectionRetryDelay < 0 || cfg.connectionRetryDelay > 5*time.Second {
		return errors.New("connection attempts must be 1-10 and retry delay 0-5s")
	}
	if cfg.messagesPerSecond > 0 && (cfg.messagePairs < 1 || cfg.messagePairs > 50 || cfg.messagePairs*2 > cfg.connections) {
		return errors.New("message pairs must be 1-50 and require two connected clients per pair")
	}
	if cfg.messagesPerSecond < 0 || cfg.messagesPerSecond > 100000 || cfg.duration <= 0 || cfg.warmup < 0 || cfg.ackWait <= 0 {
		return errors.New("invalid load duration or rate")
	}
	if cfg.maxP95 <= 0 || cfg.maxP99 < cfg.maxP95 || cfg.minimumACKRatio <= 0 || cfg.minimumACKRatio > 1 {
		return errors.New("invalid load thresholds")
	}
	if cfg.messagesPerSecond > 0 && cfg.requirePolicy && (strings.TrimSpace(cfg.businessURL) == "" || strings.TrimSpace(cfg.provisionSecret) == "" && strings.TrimSpace(cfg.otpCode) == "") {
		return errors.New("message load requires -business-api and either the development fixture secret or OTP so PolicyPlugin evaluates real business users; use -require-business-policy=false only for an isolated server without the plugin")
	}
	if cfg.messagesPerSecond > 0 && cfg.requirePolicy && strings.TrimSpace(cfg.provisionSecret) == "" && cfg.messagePairs > 5 {
		return errors.New("more than five business pairs require the development-only internal fixture so public login rate limits stay enforced")
	}
	return nil
}

func loadCredentials(cfg options) ([]string, []string) {
	uids := make([]string, cfg.connections)
	tokens := make([]string, cfg.connections)
	for index := range cfg.connections {
		uids[index] = fmt.Sprintf("load_%s_%06d", cfg.runID, index)
		tokens[index] = fmt.Sprintf("load_token_%s_%06d", cfg.runID, index)
	}
	return uids, tokens
}

type businessLoadSession struct {
	AccessToken string `json:"accessToken"`
	User        struct {
		ID string `json:"id"`
	} `json:"user"`
	IMSession struct {
		UID   string `json:"uid"`
		Token string `json:"token"`
	} `json:"imSession"`
}

func provisionBusinessPair(ctx context.Context, cfg options, uids, tokens []string) error {
	return provisionBusinessPairs(ctx, cfg, uids, tokens, 1)
}

func provisionBusinessPairs(ctx context.Context, cfg options, uids, tokens []string, pairCount int) error {
	if pairCount < 1 || len(uids) < pairCount*2 || len(tokens) < pairCount*2 {
		return errors.New("business policy load requires two clients for every message pair")
	}
	if strings.TrimSpace(cfg.provisionSecret) != "" {
		return provisionBusinessPairsInternal(ctx, cfg, uids, tokens, pairCount)
	}
	for pairIndex := 0; pairIndex < pairCount; pairIndex++ {
		if err := provisionBusinessPairAt(ctx, cfg, uids, tokens, pairIndex); err != nil {
			return fmt.Errorf("business message pair %d: %w", pairIndex, err)
		}
	}
	return nil
}

func provisionBusinessPairsInternal(ctx context.Context, cfg options, uids, tokens []string, pairCount int) error {
	var response struct {
		RunID string `json:"runId"`
		Pairs []struct {
			Sender   businessIMSession `json:"sender"`
			Receiver businessIMSession `json:"receiver"`
		} `json:"pairs"`
	}
	if err := postBusinessFixtureJSON(ctx, cfg, map[string]any{"runId": cfg.runID, "pairs": pairCount}, &response); err != nil {
		return err
	}
	if response.RunID != cfg.runID || len(response.Pairs) != pairCount {
		return errors.New("internal business fixture returned an incomplete pair set")
	}
	for pairIndex, pair := range response.Pairs {
		if pair.Sender.UID == "" || pair.Sender.Token == "" || pair.Receiver.UID == "" || pair.Receiver.Token == "" {
			return fmt.Errorf("internal business fixture pair %d has an incomplete ImSession", pairIndex)
		}
		uids[pairIndex*2], tokens[pairIndex*2] = pair.Sender.UID, pair.Sender.Token
		uids[pairIndex*2+1], tokens[pairIndex*2+1] = pair.Receiver.UID, pair.Receiver.Token
	}
	return nil
}

type businessIMSession struct {
	UID   string `json:"uid"`
	Token string `json:"token"`
}

func postBusinessFixtureJSON(ctx context.Context, cfg options, payload, output any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.businessURL, "/")+"/internal/wukong/load/pairs", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-IM-Wukong-Policy-Secret", cfg.provisionSecret)
	response, err := (&http.Client{Timeout: 2 * time.Minute}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("POST /internal/wukong/load/pairs returned %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if err = json.Unmarshal(responseBody, output); err != nil {
		return fmt.Errorf("decode internal business fixture: %w", err)
	}
	return nil
}

func provisionBusinessPairAt(ctx context.Context, cfg options, uids, tokens []string, pairIndex int) error {
	if len(uids) < 2 || len(tokens) < 2 {
		return errors.New("business policy load requires at least two clients")
	}
	senderIndex, receiverIndex := pairIndex*2, pairIndex*2+1
	senderPhone, receiverPhone := businessPairPhones(cfg.runID, pairIndex)
	login := func(phone, name string) (businessLoadSession, error) {
		var session businessLoadSession
		err := postBusinessJSON(ctx, cfg.businessURL, "/v2/auth/login", "", "android", map[string]any{
			"phone": phone, "code": cfg.otpCode, "name": name,
		}, &session)
		if err != nil {
			return session, err
		}
		if session.AccessToken == "" || session.User.ID == "" || session.IMSession.UID != session.User.ID || session.IMSession.Token == "" {
			return session, errors.New("business login returned an incomplete ImSession")
		}
		return session, nil
	}
	alice, err := login(senderPhone, fmt.Sprintf("Load policy sender %d", pairIndex))
	if err != nil {
		return fmt.Errorf("business load sender login: %w", err)
	}
	bob, err := login(receiverPhone, fmt.Sprintf("Load policy receiver %d", pairIndex))
	if err != nil {
		return fmt.Errorf("business load receiver login: %w", err)
	}
	var friend struct {
		ID string `json:"id"`
	}
	if err = postBusinessJSON(ctx, cfg.businessURL, "/v2/contacts/requests", alice.AccessToken, "", map[string]any{
		"userId": bob.User.ID, "message": "WuKong performance policy probe",
	}, &friend); err != nil {
		return fmt.Errorf("business load friend request: %w", err)
	}
	if friend.ID == "" {
		return errors.New("business load friend request id is empty")
	}
	if err = postBusinessJSON(ctx, cfg.businessURL, "/v2/contacts/requests/"+url.PathEscape(friend.ID)+"/accept", bob.AccessToken, "", map[string]any{}, nil); err != nil {
		return fmt.Errorf("business load friend accept: %w", err)
	}
	var direct struct {
		ID string `json:"id"`
	}
	if err = postBusinessJSON(ctx, cfg.businessURL, "/v2/channels/direct", alice.AccessToken, "", map[string]any{"userId": bob.User.ID}, &direct); err != nil {
		return fmt.Errorf("business load direct conversation: %w", err)
	}
	if direct.ID == "" {
		return errors.New("business load direct conversation id is empty")
	}
	uids[senderIndex], tokens[senderIndex] = alice.IMSession.UID, alice.IMSession.Token
	uids[receiverIndex], tokens[receiverIndex] = bob.IMSession.UID, bob.IMSession.Token
	return nil
}

func businessPairPhones(runID string, pairIndex int) (string, string) {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(runID))
	tail := fmt.Sprintf("%05d%03d", hash.Sum32()%100000, pairIndex)
	return "139" + tail, "138" + tail
}

func postBusinessJSON(ctx context.Context, baseURL, requestPath, token, platform string, payload any, output any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+requestPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if platform != "" {
		request.Header.Set("X-Client-Platform", platform)
	}
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("POST %s returned %d: %s", requestPath, response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if output != nil && len(bytes.TrimSpace(responseBody)) > 0 {
		if err = json.Unmarshal(responseBody, output); err != nil {
			return fmt.Errorf("decode POST %s: %w", requestPath, err)
		}
	}
	return nil
}

func provisionUsers(ctx context.Context, cfg options, uids, tokens []string) error {
	client := &http.Client{Timeout: 10 * time.Second}
	jobs := make(chan int)
	errorsChannel := make(chan error, cfg.connectionWorkers)
	var workers sync.WaitGroup
	for range cfg.connectionWorkers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				if err := provisionUser(ctx, client, cfg, uids[index], tokens[index]); err != nil {
					select {
					case errorsChannel <- err:
					default:
					}
				}
			}
		}()
	}
	for index := range uids {
		select {
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return ctx.Err()
		case jobs <- index:
		}
	}
	close(jobs)
	workers.Wait()
	close(errorsChannel)
	if err := <-errorsChannel; err != nil {
		return err
	}
	return nil
}

func provisionUser(ctx context.Context, client *http.Client, cfg options, uid, token string) error {
	payload, _ := json.Marshal(map[string]any{"uid": uid, "token": token, "device_flag": 0, "device_level": 1})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.apiURL, "/")+"/user/token", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("token", cfg.managerToken)
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("provision %s: %w", uid, err)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
	response.Body.Close()
	if readErr != nil {
		return readErr
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("provision %s returned %d: %s", uid, response.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func connectClients(ctx context.Context, cfg options, uids, tokens []string, clients []*wkclient.Client) ([]time.Duration, int, int64) {
	jobs := make(chan int)
	latencies := make(chan time.Duration, cfg.connections)
	var failures atomic.Int64
	var attempts atomic.Int64
	var workers sync.WaitGroup
	for range cfg.connectionWorkers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				started := time.Now()
				connected := false
				for attempt := 1; attempt <= cfg.connectionAttempts; attempt++ {
					attempts.Add(1)
					client := wkclient.New(cfg.tcpURL, wkclient.WithUID(uids[index]), wkclient.WithToken(tokens[index]), wkclient.WithAutoReconn(false))
					if err := client.Connect(); err == nil {
						clients[index] = client
						latencies <- time.Since(started)
						connected = true
						break
					}
					client.Close()
					if attempt < cfg.connectionAttempts && !waitForConnectionRetry(ctx, cfg.connectionRetryDelay, attempt) {
						break
					}
				}
				if !connected {
					failures.Add(1)
				}
			}
		}()
	}
	for index := range clients {
		select {
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			close(latencies)
			return collectDurations(latencies), int(failures.Load()) + len(clients) - index, attempts.Load()
		case jobs <- index:
		}
	}
	close(jobs)
	workers.Wait()
	close(latencies)
	return collectDurations(latencies), int(failures.Load()), attempts.Load()
}

func waitForConnectionRetry(ctx context.Context, base time.Duration, attempt int) bool {
	if base <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(time.Duration(attempt) * base)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func sendMessages(ctx context.Context, cfg options, senders []*wkclient.Client, recipients []string, pending []sync.Map) sendResult {
	target := int(math.Round(cfg.messagesPerSecond * cfg.duration.Seconds()))
	result := sendResult{target: target}
	started := time.Now()
	channels := make([]*wkclient.Channel, len(recipients))
	for pairIndex, recipient := range recipients {
		channels[pairIndex] = wkclient.NewChannel(recipient, 1)
	}
	payload := []byte(`{"type":1,"content":"WuKongIM load probe"}`)
	for index := 0; index < target; index++ {
		due := started.Add(time.Duration(float64(index) / cfg.messagesPerSecond * float64(time.Second)))
		if wait := time.Until(due); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				result.elapsed = time.Since(started)
				return result
			case <-timer.C:
			}
		}
		result.attempted++
		pairIndex := index % len(senders)
		clientSequence := uint64(index/len(senders) + 1)
		pending[pairIndex].Store(clientSequence, time.Now())
		clientMsgNo := fmt.Sprintf("load_%s_%09d", cfg.runID, index)
		if err := senders[pairIndex].SendMessage(channels[pairIndex], payload, wkclient.SendOptionWithClientMsgNo(clientMsgNo)); err != nil {
			pending[pairIndex].Delete(clientSequence)
			result.errors++
			continue
		}
		result.accepted++
	}
	result.elapsed = time.Since(started)
	return result
}

func closeClients(clients []*wkclient.Client) {
	for _, client := range clients {
		if client != nil {
			client.Close()
		}
	}
}

func collectDurations(input <-chan time.Duration) []time.Duration {
	items := []time.Duration{}
	for item := range input {
		items = append(items, item)
	}
	return items
}

func summarize(values []time.Duration) latencyReport {
	if len(values) == 0 {
		return latencyReport{}
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(left, right int) bool { return sorted[left] < sorted[right] })
	var total time.Duration
	for _, value := range sorted {
		total += value
	}
	return latencyReport{
		MinimumMS: milliseconds(sorted[0]),
		P50MS:     milliseconds(percentile(sorted, 0.50)),
		P95MS:     milliseconds(percentile(sorted, 0.95)),
		P99MS:     milliseconds(percentile(sorted, 0.99)),
		MaximumMS: milliseconds(sorted[len(sorted)-1]),
		MeanMS:    milliseconds(total / time.Duration(len(sorted))),
	}
}

func percentile(sorted []time.Duration, quantile float64) time.Duration {
	index := int(math.Ceil(quantile*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func milliseconds(value time.Duration) float64 {
	return float64(value) / float64(time.Millisecond)
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
