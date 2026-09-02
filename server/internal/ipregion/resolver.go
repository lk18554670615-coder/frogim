package ipregion

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/linli/im/server/internal/netutil"
	"github.com/lionsoul2014/ip2region/binding/golang/service"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
)

const Version = "ip2region-v3.17.0-cd40e3a"

var Checksums = map[string]string{
	"ip2region_v4.xdb": "6307a9696f5711f84bcb8b25f07894de68a64a0ed4a1cc7e990562dd3084f210",
	"ip2region_v6.xdb": "5b93da35ac28bc316dccc54a758381f7a874ae0461dd51ff5df5e34815586f11",
}

type Region struct {
	Status   string `json:"status"`
	Country  string `json:"country,omitempty"`
	Province string `json:"province,omitempty"`
	City     string `json:"city,omitempty"`
	ISP      string `json:"isp,omitempty"`
	Version  string `json:"version"`
}
type Resolver struct {
	lookup func(string) (string, error)
	close  func()
}

func New(dir string) *Resolver {
	r := &Resolver{}
	for name, want := range Checksums {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			slog.Warn("IP region data unavailable", "file", name)
			return r
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil || hex.EncodeToString(h.Sum(nil)) != want {
			slog.Warn("IP region checksum mismatch", "file", name)
			return r
		}
	}
	v4, err := service.NewV4Config(service.VIndexCache, filepath.Join(dir, "ip2region_v4.xdb"), 4)
	if err != nil {
		slog.Warn("IP region IPv4 setup failed")
		return r
	}
	v6, err := service.NewV6Config(service.VIndexCache, filepath.Join(dir, "ip2region_v6.xdb"), 4)
	if err != nil {
		slog.Warn("IP region IPv6 setup failed")
		return r
	}
	s, err := service.NewIp2Region(v4, v6)
	if err != nil {
		slog.Warn("IP region setup failed")
		return r
	}
	r.lookup = func(ip string) (string, error) { return s.Search(ip) }
	r.close = func() { s.Close() }
	return r
}
func (r *Resolver) Close() {
	if r != nil && r.close != nil {
		r.close()
	}
}
func (r *Resolver) Lookup(raw string) Region {
	region := Region{Status: "unavailable", Version: Version}
	ip := netutil.NormalizeIP(raw)
	if ip == "" {
		region.Status = "unknown"
		return region
	}
	addr, _ := netip.ParseAddr(ip)
	switch {
	case addr.IsLoopback():
		region.Status = "loopback"
		return region
	case addr.IsPrivate():
		region.Status = "private"
		return region
	case !addr.IsGlobalUnicast() || addr.IsLinkLocalUnicast():
		region.Status = "reserved"
		return region
	}
	if r == nil || r.lookup == nil {
		return region
	}
	v, err := r.lookup(ip)
	if err != nil {
		return region
	}
	if v == "" {
		region.Status = "not_found"
		return region
	}
	parts := strings.Split(v, "|")
	if len(parts) != 5 {
		return region
	}
	if strings.EqualFold(parts[0], "Reserved") {
		region.Status = "reserved"
		return region
	}
	clean := func(s string) string {
		if s == "0" {
			return ""
		}
		return strings.TrimSpace(s)
	}
	region.Status = "ok"
	region.Country = clean(parts[0])
	region.Province = clean(parts[1])
	region.City = clean(parts[2])
	region.ISP = clean(parts[3])
	return region
}
