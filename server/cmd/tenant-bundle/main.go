// tenant-bundle renders an operator-owned enterprise release profile.
// It never invokes Docker, publishes images, deploys or rotates existing keys.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/linli/im/server/internal/deployment"
)

func main() {
	config := flag.String("config", "", "absolute private enterprise configuration file")
	root := flag.String("output", "", "absolute pre-existing private bundle directory")
	id := flag.String("release", "", "immutable release ID")
	sequence := flag.Int64("sequence", 0, "monotonically increasing release sequence")
	rollback := flag.String("rollback-to", "", "comma-separated verified same-schema older release IDs")
	flag.Parse()
	if !filepath.IsAbs(*config) || flag.NArg() != 0 {
		fail()
	}
	f, e := os.Open(*config)
	if e != nil {
		fail()
	}
	c, e := deployment.ReadEnterpriseConfig(f)
	f.Close()
	if e != nil {
		fail()
	}
	r := deployment.Release{ID: *id, Sequence: *sequence, Runtime: "linux/amd64", SchemaVersion: 79}
	if *rollback != "" {
		r.RollbackTo = strings.Split(*rollback, ",")
	}
	r, e = deployment.WriteEnterpriseRelease(*root, c, r)
	if e != nil {
		fail()
	}
	mode := r.IsolationMode
	if mode == "" {
		mode = "local_preview"
	}
	fmt.Printf("Enterprise bundle ready: release=%s schema=%d mode=%s digest=%s\n", r.ID, r.SchemaVersion, mode, r.Digest())
}
func fail() {
	fmt.Fprintln(os.Stderr, "Bundle not generated: check private config, certificate identity, pinned images, ports and immutable output. No deployment performed.")
	os.Exit(1)
}
