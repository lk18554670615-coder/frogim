// Package linkpreview fetches small, untrusted HTML documents and extracts a
// deliberately narrow preview. Network access is fail-closed to prevent SSRF.
package linkpreview

import (
	"container/list"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	stdhtml "html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
)

const (
	defaultMaxBodyBytes      = int64(512 << 10)
	defaultMaxCacheEntries   = 256
	defaultMaxRedirects      = 3
	defaultMaxResponseHeader = int64(64 << 10)
)

var (
	ErrUnsafeURL          = errors.New("unsafe link preview URL")
	ErrUnsupportedContent = errors.New("unsupported link preview content")
	ErrResponseTooLarge   = errors.New("link preview response is too large")
)

// Preview contains only normalized plain text and a revalidated absolute image
// URL. URL is the final URL after redirects.
type Preview struct {
	URL         string `json:"url"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	ImageURL    string `json:"imageUrl,omitempty"`
}

// Resolver is the subset of net.Resolver used by Service.
type Resolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

// Config bounds all resource use. DialContext is primarily useful for tests;
// production callers should leave it nil so the service connects directly to
// a validated, pinned IP address.
type Config struct {
	TotalTimeout          time.Duration
	ResponseHeaderTimeout time.Duration
	CacheTTL              time.Duration
	MaxCacheEntries       int
	MaxBodyBytes          int64
	MaxRedirects          int
	Resolver              Resolver
	DialContext           func(context.Context, string, string) (net.Conn, error)
}

type cacheEntry struct {
	key       string
	preview   Preview
	expiresAt time.Time
}

type inflightCall struct {
	done    chan struct{}
	preview Preview
	err     error
}

type Service struct {
	client          *http.Client
	resolver        Resolver
	dial            func(context.Context, string, string) (net.Conn, error)
	totalTimeout    time.Duration
	cacheTTL        time.Duration
	maxCacheEntries int
	maxBodyBytes    int64
	maxRedirects    int

	mu       sync.Mutex
	cache    map[string]*list.Element
	lru      *list.List
	inflight map[string]*inflightCall
}

// New returns a service with conservative production defaults.
func New(config Config) *Service {
	if config.TotalTimeout <= 0 {
		config.TotalTimeout = 8 * time.Second
	}
	if config.ResponseHeaderTimeout <= 0 {
		config.ResponseHeaderTimeout = 3 * time.Second
	}
	if config.CacheTTL <= 0 {
		config.CacheTTL = 10 * time.Minute
	}
	if config.MaxCacheEntries <= 0 {
		config.MaxCacheEntries = defaultMaxCacheEntries
	}
	if config.MaxBodyBytes <= 0 || config.MaxBodyBytes > defaultMaxBodyBytes {
		config.MaxBodyBytes = defaultMaxBodyBytes
	}
	if config.MaxRedirects <= 0 || config.MaxRedirects > defaultMaxRedirects {
		config.MaxRedirects = defaultMaxRedirects
	}
	if config.Resolver == nil {
		config.Resolver = net.DefaultResolver
	}
	if config.DialContext == nil {
		dialer := &net.Dialer{Timeout: 4 * time.Second, KeepAlive: 30 * time.Second}
		config.DialContext = dialer.DialContext
	}

	service := &Service{
		resolver:        config.Resolver,
		dial:            config.DialContext,
		totalTimeout:    config.TotalTimeout,
		cacheTTL:        config.CacheTTL,
		maxCacheEntries: config.MaxCacheEntries,
		maxBodyBytes:    config.MaxBodyBytes,
		maxRedirects:    config.MaxRedirects,
		cache:           make(map[string]*list.Element),
		lru:             list.New(),
		inflight:        make(map[string]*inflightCall),
	}
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            service.dialPublic,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           32,
		MaxIdleConnsPerHost:    2,
		IdleConnTimeout:        30 * time.Second,
		TLSHandshakeTimeout:    4 * time.Second,
		ResponseHeaderTimeout:  config.ResponseHeaderTimeout,
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: defaultMaxResponseHeader,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
	}
	service.client = &http.Client{
		Transport: transport,
		Timeout:   config.TotalTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > service.maxRedirects {
				return errors.New("link preview redirect limit exceeded")
			}
			if _, err := service.validateAndResolve(req.Context(), req.URL); err != nil {
				return err
			}
			if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return fmt.Errorf("%w: HTTPS redirect downgrade", ErrUnsafeURL)
			}
			return nil
		},
	}
	return service
}

// Fetch returns a cached preview when available and coalesces concurrent fetches
// of the same canonical URL.
func (s *Service) Fetch(ctx context.Context, rawURL string) (Preview, error) {
	target, err := validateURL(rawURL)
	if err != nil {
		return Preview{}, err
	}
	key := target.String()
	now := time.Now()

	s.mu.Lock()
	if element := s.cache[key]; element != nil {
		entry := element.Value.(*cacheEntry)
		if now.Before(entry.expiresAt) {
			s.lru.MoveToFront(element)
			preview := entry.preview
			s.mu.Unlock()
			return preview, nil
		}
		s.removeCacheElement(element)
	}
	if call := s.inflight[key]; call != nil {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return Preview{}, ctx.Err()
		case <-call.done:
			return call.preview, call.err
		}
	}
	call := &inflightCall{done: make(chan struct{})}
	s.inflight[key] = call
	s.mu.Unlock()

	preview, err := s.fetch(ctx, target)

	s.mu.Lock()
	call.preview, call.err = preview, err
	delete(s.inflight, key)
	if err == nil {
		s.addCache(key, preview, time.Now().Add(s.cacheTTL))
	}
	close(call.done)
	s.mu.Unlock()
	return preview, err
}

func (s *Service) fetch(parent context.Context, target *url.URL) (Preview, error) {
	ctx, cancel := context.WithTimeout(parent, s.totalTimeout)
	defer cancel()
	if _, err := s.validateAndResolve(ctx, target); err != nil {
		return Preview{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return Preview{}, err
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", "Qingwaguagua-LinkPreview/1.0")
	res, err := s.client.Do(req)
	if err != nil {
		return Preview{}, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return Preview{}, fmt.Errorf("link preview HTTP status %d", res.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "text/html") {
		return Preview{}, ErrUnsupportedContent
	}
	if res.ContentLength > s.maxBodyBytes {
		return Preview{}, ErrResponseTooLarge
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, s.maxBodyBytes+1))
	if err != nil {
		return Preview{}, err
	}
	if int64(len(body)) > s.maxBodyBytes {
		return Preview{}, ErrResponseTooLarge
	}
	document, err := xhtml.Parse(strings.NewReader(string(body)))
	if err != nil {
		return Preview{}, ErrUnsupportedContent
	}
	title, description, image := extract(document)
	preview := Preview{
		URL:         res.Request.URL.String(),
		Title:       cleanText(title, 300),
		Description: cleanText(description, 1000),
	}
	if image != "" {
		if reference, parseErr := url.Parse(strings.TrimSpace(image)); parseErr == nil {
			absolute := res.Request.URL.ResolveReference(reference)
			absolute.Fragment = ""
			if _, validationErr := s.validateAndResolve(ctx, absolute); validationErr == nil {
				preview.ImageURL = absolute.String()
			}
		}
	}
	return preview, nil
}

func (s *Service) dialPublic(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || (port != "80" && port != "443") {
		return nil, fmt.Errorf("%w: invalid network address", ErrUnsafeURL)
	}
	ips, err := s.resolvePublic(ctx, host)
	if err != nil {
		return nil, err
	}
	var dialErrors []error
	for _, ip := range ips {
		conn, dialErr := s.dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr != nil {
			dialErrors = append(dialErrors, dialErr)
			continue
		}
		actual, actualErr := remoteIP(conn.RemoteAddr())
		if actualErr != nil || !isPublicIP(actual) {
			_ = conn.Close()
			return nil, fmt.Errorf("%w: connected endpoint is not public", ErrUnsafeURL)
		}
		return conn, nil
	}
	if len(dialErrors) == 0 {
		return nil, fmt.Errorf("%w: hostname has no addresses", ErrUnsafeURL)
	}
	return nil, fmt.Errorf("link preview connection failed: %w", errors.Join(dialErrors...))
}

func (s *Service) validateAndResolve(ctx context.Context, target *url.URL) ([]netip.Addr, error) {
	if err := validateParsedURL(target); err != nil {
		return nil, err
	}
	return s.resolvePublic(ctx, target.Hostname())
}

func (s *Service) resolvePublic(ctx context.Context, host string) ([]netip.Addr, error) {
	if strings.Contains(host, "%") {
		return nil, fmt.Errorf("%w: IPv6 zones are not allowed", ErrUnsafeURL)
	}
	if literal, err := netip.ParseAddr(host); err == nil {
		if literal.Is4In6() || !isPublicIP(literal) {
			return nil, fmt.Errorf("%w: IP address is not public", ErrUnsafeURL)
		}
		return []netip.Addr{literal}, nil
	}
	addresses, err := s.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve link preview host: %w", err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("%w: hostname has no addresses", ErrUnsafeURL)
	}
	result := make([]netip.Addr, 0, len(addresses))
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		if address.Zone != "" || isMappedIP(address.IP) {
			return nil, fmt.Errorf("%w: resolved address is not public", ErrUnsafeURL)
		}
		parsed, ok := netip.AddrFromSlice(address.IP)
		if !ok {
			return nil, fmt.Errorf("%w: invalid resolved address", ErrUnsafeURL)
		}
		parsed = parsed.Unmap()
		if !isPublicIP(parsed) {
			return nil, fmt.Errorf("%w: resolved address is not public", ErrUnsafeURL)
		}
		if _, exists := seen[parsed]; !exists {
			seen[parsed] = struct{}{}
			result = append(result, parsed)
		}
	}
	return result, nil
}

func validateURL(rawURL string) (*url.URL, error) {
	if rawURL == "" || strings.TrimSpace(rawURL) != rawURL || !utf8.ValidString(rawURL) {
		return nil, ErrUnsafeURL
	}
	target, err := url.Parse(rawURL)
	if err != nil {
		return nil, ErrUnsafeURL
	}
	target.Fragment = ""
	if err = validateParsedURL(target); err != nil {
		return nil, err
	}
	return target, nil
}

func validateParsedURL(target *url.URL) error {
	if target == nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" || target.Hostname() == "" {
		return ErrUnsafeURL
	}
	if target.User != nil || target.Opaque != "" {
		return fmt.Errorf("%w: userinfo and opaque URLs are not allowed", ErrUnsafeURL)
	}
	if strings.ContainsAny(target.Hostname(), "\x00\r\n\t") {
		return ErrUnsafeURL
	}
	port := target.Port()
	if port == "" {
		return nil
	}
	if (target.Scheme == "http" && port != "80") || (target.Scheme == "https" && port != "443") {
		return fmt.Errorf("%w: only the default HTTP and HTTPS ports are allowed", ErrUnsafeURL)
	}
	return nil
}

func remoteIP(address net.Addr) (netip.Addr, error) {
	if address == nil {
		return netip.Addr{}, ErrUnsafeURL
	}
	if tcp, ok := address.(*net.TCPAddr); ok {
		parsed, ok := netip.AddrFromSlice(tcp.IP)
		if !ok || isMappedIP(tcp.IP) {
			return netip.Addr{}, ErrUnsafeURL
		}
		return parsed.Unmap(), nil
	}
	host, _, err := net.SplitHostPort(address.String())
	if err != nil {
		return netip.Addr{}, ErrUnsafeURL
	}
	parsed, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil || parsed.Is4In6() {
		return netip.Addr{}, ErrUnsafeURL
	}
	return parsed, nil
}

func isMappedIP(ip net.IP) bool {
	return len(ip) == net.IPv6len && ip[0] == 0 && ip[1] == 0 && ip[2] == 0 && ip[3] == 0 &&
		ip[4] == 0 && ip[5] == 0 && ip[6] == 0 && ip[7] == 0 && ip[8] == 0 && ip[9] == 0 &&
		ip[10] == 0xff && ip[11] == 0xff
}

var deniedRanges = mustPrefixes(
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
	"172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15",
	"198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
	"::/96", "::1/128", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/23",
	"2001:db8::/32", "2002::/16", "3fff::/20", "5f00::/16", "fc00::/7", "fe80::/10", "ff00::/8",
)

var metadataAddresses = mustAddresses("169.254.169.254", "169.254.170.2", "169.254.169.123", "100.100.100.200", "168.63.129.16")

func isPublicIP(address netip.Addr) bool {
	if !address.IsValid() || address.Is4In6() {
		return false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() {
		return false
	}
	if _, denied := metadataAddresses[address]; denied {
		return false
	}
	for _, prefix := range deniedRanges {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func mustPrefixes(values ...string) []netip.Prefix {
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		result = append(result, netip.MustParsePrefix(value))
	}
	return result
}

func mustAddresses(values ...string) map[netip.Addr]struct{} {
	result := make(map[netip.Addr]struct{}, len(values))
	for _, value := range values {
		result[netip.MustParseAddr(value)] = struct{}{}
	}
	return result
}

func extract(root *xhtml.Node) (title, description, image string) {
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode {
			switch strings.ToLower(node.Data) {
			case "title":
				if title == "" {
					title = nodeText(node)
				}
			case "meta":
				attributes := make(map[string]string, len(node.Attr))
				for _, attribute := range node.Attr {
					attributes[strings.ToLower(attribute.Key)] = attribute.Val
				}
				key := strings.ToLower(strings.TrimSpace(firstNonEmpty(attributes["property"], attributes["name"])))
				switch key {
				case "description", "og:description":
					if description == "" {
						description = attributes["content"]
					}
				case "og:image", "og:image:url", "twitter:image":
					if image == "" {
						image = attributes["content"]
					}
				}
			case "script", "style", "noscript", "template":
				return
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return title, description, image
}

func nodeText(node *xhtml.Node) string {
	var parts []string
	var walk func(*xhtml.Node)
	walk = func(current *xhtml.Node) {
		if current.Type == xhtml.ElementNode {
			switch strings.ToLower(current.Data) {
			case "script", "style", "noscript", "template":
				return
			}
		}
		if current.Type == xhtml.TextNode {
			parts = append(parts, current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.Join(parts, " ")
}

func cleanText(value string, maxRunes int) string {
	// Treat extracted strings as untrusted markup once more. This removes script,
	// style, template and noscript content even when those tags were entity-escaped
	// inside a title or meta content attribute.
	if document, err := xhtml.Parse(strings.NewReader("<!doctype html><body>" + value + "</body>")); err == nil {
		value = nodeText(document)
	}
	value = stdhtml.UnescapeString(value)
	var builder strings.Builder
	builder.Grow(len(value))
	space := true
	count := 0
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			if !space && count < maxRunes {
				builder.WriteByte(' ')
				count++
			}
			space = true
			continue
		}
		if count >= maxRunes {
			break
		}
		builder.WriteRune(r)
		count++
		space = false
	}
	return strings.TrimSpace(builder.String())
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (s *Service) addCache(key string, preview Preview, expiresAt time.Time) {
	if element := s.cache[key]; element != nil {
		entry := element.Value.(*cacheEntry)
		entry.preview, entry.expiresAt = preview, expiresAt
		s.lru.MoveToFront(element)
		return
	}
	element := s.lru.PushFront(&cacheEntry{key: key, preview: preview, expiresAt: expiresAt})
	s.cache[key] = element
	for s.lru.Len() > s.maxCacheEntries {
		s.removeCacheElement(s.lru.Back())
	}
}

func (s *Service) removeCacheElement(element *list.Element) {
	if element == nil {
		return
	}
	delete(s.cache, element.Value.(*cacheEntry).key)
	s.lru.Remove(element)
}
