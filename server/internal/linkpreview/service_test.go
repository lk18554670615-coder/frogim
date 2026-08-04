package linkpreview

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type staticResolver struct {
	addresses map[string][]string
	calls     atomic.Int32
}

func (r *staticResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	r.calls.Add(1)
	values, ok := r.addresses[host]
	if !ok {
		return nil, fmt.Errorf("unknown test host %q", host)
	}
	result := make([]net.IPAddr, 0, len(values))
	for _, value := range values {
		address := netip.MustParseAddr(value)
		result = append(result, net.IPAddr{IP: net.IP(address.AsSlice())})
	}
	return result, nil
}

type remoteAddrConn struct {
	net.Conn
	remote net.Addr
}

func (c remoteAddrConn) RemoteAddr() net.Addr { return c.remote }

func serverDialer(server *httptest.Server, remoteIP string) func(context.Context, string, string) (net.Conn, error) {
	serverAddress := strings.TrimPrefix(server.URL, "http://")
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, serverAddress)
		if err != nil {
			return nil, err
		}
		return remoteAddrConn{Conn: conn, remote: &net.TCPAddr{IP: net.ParseIP(remoteIP).To4(), Port: 80}}, nil
	}
}

func testService(server *httptest.Server, resolver Resolver, mutate func(*Config)) *Service {
	config := Config{
		TotalTimeout:          2 * time.Second,
		ResponseHeaderTimeout: time.Second,
		CacheTTL:              time.Minute,
		Resolver:              resolver,
		DialContext:           serverDialer(server, "93.184.216.34"),
	}
	if mutate != nil {
		mutate(&config)
	}
	return New(config)
}

func TestPublicIPPolicyRejectsSpecialAndMetadataRanges(t *testing.T) {
	tests := map[string]bool{
		"8.8.8.8":              true,
		"2606:4700:4700::1111": true,
		"0.0.0.0":              false,
		"10.0.0.1":             false,
		"100.64.0.1":           false,
		"100.100.100.200":      false,
		"127.0.0.1":            false,
		"168.63.129.16":        false,
		"169.254.169.254":      false,
		"192.0.2.1":            false,
		"198.18.0.1":           false,
		"198.51.100.1":         false,
		"203.0.113.1":          false,
		"224.0.0.1":            false,
		"::1":                  false,
		"64:ff9b::7f00:1":      false,
		"2001:20::1":           false,
		"2001:db8::1":          false,
		"2002:7f00:1::":        false,
		"3fff::1":              false,
		"fc00::1":              false,
		"fe80::1":              false,
		"ff02::1":              false,
		"::ffff:8.8.8.8":       false,
	}
	for raw, want := range tests {
		if got := isPublicIP(netip.MustParseAddr(raw)); got != want {
			t.Errorf("isPublicIP(%s)=%v want %v", raw, got, want)
		}
	}
}

func TestURLPolicyRejectsCredentialsSchemesAndNonDefaultPorts(t *testing.T) {
	for _, raw := range []string{
		"file:///etc/passwd",
		"ftp://example.com/file",
		"http://user:pass@example.com/",
		"http://example.com:8080/",
		"https://example.com:8443/",
		" HTTP://example.com/",
	} {
		if _, err := validateURL(raw); !errors.Is(err, ErrUnsafeURL) {
			t.Errorf("validateURL(%q) error=%v", raw, err)
		}
	}
	for _, raw := range []string{"http://example.com:80/", "https://example.com:443/"} {
		if _, err := validateURL(raw); err != nil {
			t.Errorf("validateURL(%q) error=%v", raw, err)
		}
	}
}

func TestFetchRejectsEveryUnsafeDNSAnswerBeforeDial(t *testing.T) {
	resolver := &staticResolver{addresses: map[string][]string{
		"private.test": {"10.0.0.2"},
		"mixed.test":   {"93.184.216.34", "127.0.0.1"},
	}}
	var dialCalls atomic.Int32
	service := New(Config{
		Resolver: resolver,
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			dialCalls.Add(1)
			return nil, errors.New("must not dial")
		},
	})
	for _, raw := range []string{
		"http://private.test/",
		"http://mixed.test/",
		"http://127.0.0.1/",
		"http://169.254.169.254/latest/meta-data/",
		"http://100.100.100.200/",
		"http://192.0.2.10/",
		"http://198.18.0.1/",
		"http://[::ffff:127.0.0.1]/",
	} {
		if _, err := service.Fetch(context.Background(), raw); !errors.Is(err, ErrUnsafeURL) {
			t.Errorf("Fetch(%q) error=%v", raw, err)
		}
	}
	if dialCalls.Load() != 0 {
		t.Fatalf("unsafe targets reached dialer %d times", dialCalls.Load())
	}
}

func TestDialRejectsNonPublicActualConnectionTarget(t *testing.T) {
	resolver := &staticResolver{addresses: map[string][]string{"public.test": {"93.184.216.34"}}}
	service := New(Config{
		Resolver: resolver,
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			client, peer := net.Pipe()
			go peer.Close()
			return remoteAddrConn{Conn: client, remote: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 80}}, nil
		},
	})
	_, err := service.Fetch(context.Background(), "http://public.test/")
	if !errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("actual private target error=%v", err)
	}
}

func TestRedirectsAreRevalidatedAndLimitedToThree(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/unsafe":
			http.Redirect(w, r, "http://127.0.0.1/latest/meta-data/", http.StatusFound)
		case "/0", "/1", "/2", "/3":
			step := int(r.URL.Path[1] - '0')
			http.Redirect(w, r, fmt.Sprintf("/%d", step+1), http.StatusFound)
		case "/ok0", "/ok1", "/ok2":
			step := int(r.URL.Path[len(r.URL.Path)-1] - '0')
			http.Redirect(w, r, fmt.Sprintf("/ok%d", step+1), http.StatusFound)
		case "/ok3":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<title>three redirects</title>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	resolver := &staticResolver{addresses: map[string][]string{"preview.test": {"93.184.216.34"}}}
	service := testService(server, resolver, nil)

	if _, err := service.Fetch(context.Background(), "http://preview.test/unsafe"); !errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("unsafe redirect error=%v", err)
	}
	if _, err := service.Fetch(context.Background(), "http://preview.test/0"); err == nil || !strings.Contains(err.Error(), "redirect limit") {
		t.Fatalf("fourth redirect error=%v", err)
	}
	preview, err := service.Fetch(context.Background(), "http://preview.test/ok0")
	if err != nil || preview.Title != "three redirects" || !strings.HasSuffix(preview.URL, "/ok3") {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
}

func TestFetchRejectsOversizeAndNonHTMLResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"title":"not html"}`))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(strings.Repeat("x", 65)))
	}))
	defer server.Close()
	resolver := &staticResolver{addresses: map[string][]string{"preview.test": {"93.184.216.34"}}}
	service := testService(server, resolver, func(config *Config) { config.MaxBodyBytes = 64 })
	if _, err := service.Fetch(context.Background(), "http://preview.test/json"); !errors.Is(err, ErrUnsupportedContent) {
		t.Fatalf("content type error=%v", err)
	}
	if _, err := service.Fetch(context.Background(), "http://preview.test/large"); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("oversize error=%v", err)
	}
}

func TestExtractsPlainTextAndRevalidatesAbsoluteImageURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><head>
			<title> Safe &amp; calm &lt;script&gt;titleLeak()&lt;/script&gt; </title>
			<meta name="description" content="Useful &lt;script&gt;descriptionLeak()&lt;/script&gt; summary">
			<meta property="og:image" content="/assets/cover.png">
			<script>pageLeak()</script><style>.secret{}</style>
		</head></html>`))
	}))
	defer server.Close()
	resolver := &staticResolver{addresses: map[string][]string{"preview.test": {"93.184.216.34"}}}
	service := testService(server, resolver, nil)
	preview, err := service.Fetch(context.Background(), "http://preview.test/article")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Title != "Safe & calm" || preview.Description != "Useful summary" {
		t.Fatalf("unsafe or incorrect text preview=%+v", preview)
	}
	if preview.ImageURL != "http://preview.test/assets/cover.png" {
		t.Fatalf("image URL=%q", preview.ImageURL)
	}
	for _, secret := range []string{"titleLeak", "descriptionLeak", "pageLeak", "<script"} {
		if strings.Contains(fmt.Sprintf("%+v", preview), secret) {
			t.Fatalf("script content leaked: %+v", preview)
		}
	}
}

func TestUnsafePreviewImageIsOmitted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<title>safe page</title><meta property="og:image" content="http://image.test/private.png">`))
	}))
	defer server.Close()
	resolver := &staticResolver{addresses: map[string][]string{
		"preview.test": {"93.184.216.34"},
		"image.test":   {"10.0.0.9"},
	}}
	service := testService(server, resolver, nil)
	preview, err := service.Fetch(context.Background(), "http://preview.test/article")
	if err != nil || preview.Title != "safe page" || preview.ImageURL != "" {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
}

func TestCacheTTLBoundAndConcurrentRequestCoalescing(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		time.Sleep(30 * time.Millisecond)
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprintf(w, "<title>%s</title>", r.URL.Path)
	}))
	defer server.Close()
	resolver := &staticResolver{addresses: map[string][]string{"preview.test": {"93.184.216.34"}}}
	service := testService(server, resolver, func(config *Config) {
		config.CacheTTL = 50 * time.Millisecond
		config.MaxCacheEntries = 2
	})

	const clients = 12
	var wg sync.WaitGroup
	errorsSeen := make(chan error, clients)
	for range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			preview, err := service.Fetch(context.Background(), "http://preview.test/shared")
			if err != nil {
				errorsSeen <- err
			} else if preview.Title != "/shared" {
				errorsSeen <- fmt.Errorf("title=%q", preview.Title)
			}
		}()
	}
	wg.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Error(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("concurrent requests=%d", requests.Load())
	}
	if _, err := service.Fetch(context.Background(), "http://preview.test/shared"); err != nil || requests.Load() != 1 {
		t.Fatalf("cache miss err=%v requests=%d", err, requests.Load())
	}
	time.Sleep(60 * time.Millisecond)
	if _, err := service.Fetch(context.Background(), "http://preview.test/shared"); err != nil || requests.Load() != 2 {
		t.Fatalf("expired cache err=%v requests=%d", err, requests.Load())
	}
	for _, path := range []string{"one", "two", "three"} {
		if _, err := service.Fetch(context.Background(), "http://preview.test/"+path); err != nil {
			t.Fatal(err)
		}
	}
	service.mu.Lock()
	cacheSize := len(service.cache)
	service.mu.Unlock()
	if cacheSize != 2 {
		t.Fatalf("cache size=%d", cacheSize)
	}
}
