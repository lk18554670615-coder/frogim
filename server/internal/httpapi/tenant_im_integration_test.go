package httpapi

import (
	"bufio"
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/linli/im/server/internal/tenancy"
	"github.com/linli/im/server/internal/wukong"
	"golang.org/x/net/websocket"
)

// Minimal CONNECT/CONNACK probe from the pinned Flutter v4 codec. No messages
// are sent and no tokens or packet bytes are logged. Ports are disposable only.
type tenancyIMProbe struct {
	closed        chan struct{}
	kicked        chan struct{}
	pongs         atomic.Int64
	protocolError atomic.Bool
}

func tenancyStackIMConnect(t *testing.T, session map[string]any, accept bool) *tenancyIMProbe {
	t.Helper()
	im := session["imSession"].(map[string]any)
	endpoint, e := url.Parse(im["tcpUrl"].(string))
	internalProbe := os.Getenv("TENANCY_IM_TRANSPORT_TEST") == "linux" && runtime.GOOS == "linux" && endpoint != nil && endpoint.Host == "127.0.0.1:5100"
	if e != nil || endpoint.Scheme != "tcp" || (!internalProbe && endpoint.Host != "127.0.0.1:15174" && endpoint.Host != "127.0.0.1:15175") {
		t.Fatal("not an isolated IM endpoint")
	}
	conn, e := net.DialTimeout("tcp", endpoint.Host, 5*time.Second)
	if e != nil {
		t.Fatal("isolated IM dial failed")
	}
	return tenancyStackIMHandshake(t, conn, im, accept)
}

// The disposable IM's loopback WS listener sits behind a temporary, verified
// TLS gateway. This exercises actual binary WSS, not a mocked IM connection.
// Runtime Caddy/JS SDK connectivity is checked separately by the local probe.
func tenancyStackIMWSSConnect(t *testing.T, session map[string]any, accept bool) *tenancyIMProbe {
	t.Helper()
	im := session["imSession"].(map[string]any)
	endpoint, e := url.Parse(im["tcpUrl"].(string))
	if e != nil || endpoint.Scheme != "tcp" || (endpoint.Host != "127.0.0.1:15174" && endpoint.Host != "127.0.0.1:15175") {
		t.Fatal("not an isolated WSS endpoint")
	}
	port := "15274"
	if endpoint.Port() == "15175" {
		port = "15275"
	}
	target, _ := url.Parse("http://127.0.0.1:" + port)
	proxy := httptest.NewTLSServer(httputil.NewSingleHostReverseProxy(target))
	t.Cleanup(proxy.Close)
	roots := x509.NewCertPool()
	roots.AddCert(proxy.Certificate())
	cfg, e := websocket.NewConfig("wss"+strings.TrimPrefix(proxy.URL, "https")+"/", proxy.URL)
	if e != nil {
		t.Fatal("WSS fixture configuration")
	}
	cfg.TlsConfig = &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots}
	cfg.Dialer = &net.Dialer{Timeout: 5 * time.Second}
	conn, e := websocket.DialConfig(cfg)
	if e != nil {
		t.Fatal("isolated verified WSS dial failed")
	}
	conn.PayloadType = websocket.BinaryFrame
	return tenancyStackIMHandshake(t, conn, im, accept)
}

func tenancyStackIMHandshake(t *testing.T, conn net.Conn, im map[string]any, accept bool) *tenancyIMProbe {
	t.Helper()
	t.Cleanup(func() { _ = conn.Close() })
	key, e := ecdh.X25519().GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal("fixture key generation")
	}
	var body bytes.Buffer
	body.WriteByte(4)
	body.WriteByte(byte(im["deviceFlag"].(float64)))
	field := func(v string) { _ = binary.Write(&body, binary.BigEndian, uint16(len(v))); body.WriteString(v) }
	field("isolated-realm-test")
	field(im["uid"].(string))
	field(im["token"].(string))
	_ = binary.Write(&body, binary.BigEndian, uint64(time.Now().UnixMilli()))
	field(base64.StdEncoding.EncodeToString(key.PublicKey().Bytes()))
	frame := []byte{0x10}
	n := body.Len()
	for {
		b := byte(n % 128)
		n /= 128
		if n > 0 {
			b |= 128
		}
		frame = append(frame, b)
		if n == 0 {
			break
		}
	}
	frame = append(frame, body.Bytes()...)
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, e = conn.Write(frame); e != nil {
		t.Fatal("IM CONNECT write failed")
	}
	reader := bufio.NewReader(conn)
	header, e := reader.ReadByte()
	// The pinned server may close an unauthenticated socket instead of
	// returning a negative CONNACK. A timeout is NOT an accepted rejection.
	if !accept && e == io.EOF {
		_ = conn.Close()
		probe := &tenancyIMProbe{closed: make(chan struct{}), kicked: make(chan struct{})}
		close(probe.closed)
		return probe
	}
	if e != nil || header>>4 != 2 {
		t.Fatal("IM did not return CONNACK", e, header>>4)
	}
	size := 0
	for shift := 0; shift < 28; shift += 7 {
		b, e := reader.ReadByte()
		if e != nil {
			t.Fatal("invalid CONNACK length")
		}
		size |= int(b&127) << shift
		if b&128 == 0 {
			break
		}
	}
	if size < 9 || size > 65536 {
		t.Fatal("invalid CONNACK size")
	}
	reply := make([]byte, size)
	if _, e = io.ReadFull(reader, reply); e != nil {
		t.Fatal("incomplete CONNACK")
	}
	offset := 8
	if header&1 != 0 {
		offset++
	}
	if offset >= len(reply) {
		t.Fatal("missing CONNACK reason")
	}
	if (reply[offset] == 1) != accept {
		t.Fatal("unexpected IM acceptance", accept, int(reply[offset]))
	}
	_ = conn.SetDeadline(time.Time{})
	probe := &tenancyIMProbe{closed: make(chan struct{}), kicked: make(chan struct{})}
	if !accept {
		_ = conn.Close()
		close(probe.closed)
		return probe
	}
	go func() {
		defer close(probe.closed)
		var kicked sync.Once
		for {
			header, err := reader.ReadByte()
			if err != nil {
				return
			}
			if header>>4 == 8 {
				probe.pongs.Add(1)
				continue
			} // PONG has no remaining length.
			size := 0
			for shift := 0; ; shift += 7 {
				if shift >= 28 {
					probe.protocolError.Store(true)
					return
				}
				b, err := reader.ReadByte()
				if err != nil {
					return
				}
				size |= int(b&127) << shift
				if b&128 == 0 {
					break
				}
			}
			if size > 65536 {
				probe.protocolError.Store(true)
				return
			}
			if _, err := io.CopyN(io.Discard, reader, int64(size)); err != nil {
				return
			}
			if header>>4 == 9 {
				if size < 3 {
					probe.protocolError.Store(true)
					return
				}
				kicked.Do(func() { close(probe.kicked) })
			}
		}
	}()
	go func() {
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-probe.closed:
				return
			case <-tick.C:
				_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
				if _, err := conn.Write([]byte{0x70}); err != nil {
					_ = conn.Close()
					return
				}
			}
		}
	}()
	return probe
}

func (p *tenancyIMProbe) assertClosed(t *testing.T) {
	t.Helper()
	select {
	case <-p.closed:
		if p.protocolError.Load() {
			t.Fatal("malformed packet is not a confirmed transport close")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("physical IM transport not closed")
	}
}

func (p *tenancyIMProbe) assertAlive(t *testing.T) {
	t.Helper()
	before := p.pongs.Load()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-p.closed:
			t.Fatal("expected live IM connection closed")
		case <-deadline.C:
			t.Fatal("expected live IM connection did not answer heartbeat")
		case <-tick.C:
			if p.pongs.Load() > before {
				return
			}
		}
	}
}

// Optional paired host/container probe isolates Docker port forwarding from
// the pinned IM transport. Only run against the disposable test IM instance.
func TestTenantStackIMTransportClose(t *testing.T) {
	mode := os.Getenv("TENANCY_IM_TRANSPORT_TEST")
	if mode == "" {
		t.Skip("explicit disposable IM transport probe not requested")
	}
	api, tcp := "http://127.0.0.1:15575", "tcp://127.0.0.1:15175"
	if mode == "linux" && runtime.GOOS == "linux" {
		api, tcp = "http://127.0.0.1:5001", "tcp://127.0.0.1:5100"
	} else if mode != "host" {
		t.Fatal("invalid transport probe environment")
	}
	client, err := wukong.NewClient(wukong.Config{APIURL: api, ManagerURL: api, ManagerToken: "isolated-test-only"})
	if err != nil {
		t.Fatal("transport client configuration")
	}
	transports := []string{"tcp"}
	if mode == "host" {
		transports = append(transports, "wss")
	}
	for _, transport := range transports {
		t.Run(transport, func(t *testing.T) {
			uid, _ := tenancy.Secret()
			token, _ := tenancy.Secret()
			if err := client.ProvisionUser(t.Context(), wukong.UserTokenRequest{UID: uid, Token: token, DeviceFlag: wukong.DeviceApp, DeviceLevel: wukong.DeviceLevelMaster}); err != nil {
				t.Fatal("transport fixture provisioning")
			}
			dial := tenancyStackIMConnect
			if transport == "wss" {
				dial = tenancyStackIMWSSConnect
			}
			probe := dial(t, map[string]any{"imSession": map[string]any{"uid": uid, "token": token, "deviceFlag": float64(0), "tcpUrl": tcp}}, true)
			probe.assertAlive(t)
			if err := client.QuitDevice(t.Context(), uid, -1); err != nil {
				t.Fatal("transport quit request")
			}
			select {
			case <-probe.kicked:
			case <-time.After(5 * time.Second):
				t.Fatal("no DISCONNECT")
			}
			probe.assertClosed(t)
		})
	}
}
