// Downloads pinned build inputs only. The running server never queries the
// internet for geolocation. Run from server/: go run ./cmd/prepare-ipregion
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	out := flag.String("out", ".data/ip2region", "output directory")
	lock := flag.String("lock", "ipregion.lock.json", "pinned manifest")
	flag.Parse()
	raw, err := os.ReadFile(*lock)
	if err != nil {
		return err
	}
	var m struct {
		Commit string
		Files  []struct{ Name, SHA256 string }
	}
	if err = json.Unmarshal(raw, &m); err != nil {
		return err
	}
	if len(m.Commit) != 40 {
		return fmt.Errorf("invalid commit")
	}
	if err = os.MkdirAll(*out, 0755); err != nil {
		return err
	}
	client := &http.Client{Timeout: 3 * time.Minute}
	for _, f := range m.Files {
		if filepath.Base(f.Name) != f.Name {
			return fmt.Errorf("invalid filename")
		}
		path := filepath.Join(*out, f.Name)
		if old, e := os.Open(path); e == nil {
			h := sha256.New()
			_, e = io.Copy(h, old)
			old.Close()
			if e == nil && hex.EncodeToString(h.Sum(nil)) == f.SHA256 {
				fmt.Println("verified", f.Name)
				continue
			}
		}
		sourcePath := "data/" + f.Name
		if f.Name == "LICENSE.md" {
			sourcePath = f.Name
		}
		resp, e := client.Get("https://raw.githubusercontent.com/lionsoul2014/ip2region/" + m.Commit + "/" + sourcePath)
		if e != nil {
			return e
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return fmt.Errorf("download %s: HTTP %d", f.Name, resp.StatusCode)
		}
		tmp, e := os.CreateTemp(*out, ".ipregion-*")
		if e != nil {
			resp.Body.Close()
			return e
		}
		tmpName := tmp.Name()
		h := sha256.New()
		_, e = io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, 128<<20))
		resp.Body.Close()
		tmp.Close()
		if e != nil || hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
			os.Remove(tmpName)
			return fmt.Errorf("checksum failed: %s", f.Name)
		}
		if e = os.Rename(tmpName, path); e != nil {
			os.Remove(tmpName)
			return e
		}
		fmt.Println("prepared", f.Name)
	}
	return os.WriteFile(filepath.Join(*out, "ipregion.lock.json"), raw, 0644)
}
