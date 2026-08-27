package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/WuKongIM/go-pdk/pdk"
)

const (
	pluginNo           = "wk.plugin.im-policy"
	pluginVersion      = "1.0.0"
	pluginPriority     = int32(1)
	reasonSuccess      = uint32(1)
	reasonSystemError  = uint32(15)
	policySecretHeader = "X-IM-Wukong-Policy-Secret"
)

func main() {
	if err := pdk.RunServer(newPolicyPlugin, pluginNo, pdk.WithVersion(pluginVersion), pdk.WithPriority(pluginPriority)); err != nil {
		panic(err)
	}
}

type policyPlugin struct {
	endpoint string
	secret   string
	client   *http.Client
}

type policyRequest struct {
	FromUID     string `json:"fromUid"`
	ChannelID   string `json:"channelId"`
	ChannelType uint8  `json:"channelType"`
	Payload     []byte `json:"payload"`
	DeviceID    string `json:"deviceId,omitempty"`
	DeviceFlag  uint32 `json:"deviceFlag,omitempty"`
	DeviceLevel uint32 `json:"deviceLevel,omitempty"`
}

type policyResponse struct {
	Allowed    bool   `json:"allowed"`
	ReasonCode uint8  `json:"reasonCode"`
	Code       string `json:"code"`
}

func newPolicyPlugin() interface{} {
	timeout := 500 * time.Millisecond
	if configured, err := time.ParseDuration(strings.TrimSpace(os.Getenv("IM_WUKONG_POLICY_TIMEOUT"))); err == nil && configured >= 50*time.Millisecond && configured <= 900*time.Millisecond {
		timeout = configured
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 200
	transport.MaxIdleConnsPerHost = 200
	transport.IdleConnTimeout = 90 * time.Second
	transport.ResponseHeaderTimeout = timeout
	return &policyPlugin{
		endpoint: strings.TrimSpace(os.Getenv("IM_WUKONG_POLICY_URL")),
		secret:   os.Getenv("IM_WUKONG_POLICY_SECRET"),
		client:   &http.Client{Transport: transport, Timeout: timeout},
	}
}

// Send is invoked synchronously by WuKongIM after payload decryption and
// before channel dispatch. Every failure is intentionally fail-closed.
func (p *policyPlugin) Send(context *pdk.Context) {
	if context == nil || context.SendPacket == nil {
		return
	}
	reason := reasonSystemError
	defer func() {
		if recover() != nil {
			reason = reasonSystemError
		}
		context.SendPacket.Reason = reason
	}()
	packet := context.SendPacket
	request := policyRequest{
		FromUID: packet.FromUid, ChannelID: packet.ChannelId,
		ChannelType: uint8(packet.ChannelType), Payload: append([]byte(nil), packet.Payload...),
	}
	if packet.ChannelType > 255 {
		return
	}
	if packet.Conn != nil {
		request.DeviceID = packet.Conn.DeviceId
		request.DeviceFlag = packet.Conn.DeviceFlag
		request.DeviceLevel = packet.Conn.DeviceLevel
	}
	reason = p.decide(request)
}

func (p *policyPlugin) decide(input policyRequest) uint32 {
	if p == nil || p.client == nil || len(p.secret) < 32 {
		return reasonSystemError
	}
	parsed, err := url.Parse(p.endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return reasonSystemError
	}
	body, err := json.Marshal(input)
	if err != nil {
		return reasonSystemError
	}
	request, err := http.NewRequest(http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return reasonSystemError
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(policySecretHeader, p.secret)
	response, err := p.client.Do(request)
	if err != nil {
		return reasonSystemError
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 8<<10))
		return reasonSystemError
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8<<10))
	decoder.DisallowUnknownFields()
	var decision policyResponse
	if err = decoder.Decode(&decision); err != nil || hasTrailingJSON(decoder) {
		return reasonSystemError
	}
	if decision.Allowed {
		if decision.ReasonCode != uint8(reasonSuccess) {
			return reasonSystemError
		}
		return reasonSuccess
	}
	if decision.ReasonCode <= uint8(reasonSuccess) {
		return reasonSystemError
	}
	return uint32(decision.ReasonCode)
}

func hasTrailingJSON(decoder *json.Decoder) bool {
	var trailing any
	err := decoder.Decode(&trailing)
	return !errors.Is(err, io.EOF)
}
