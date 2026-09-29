// Offline operator renderer. It never invokes Docker or accesses live services.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/linli/im/server/internal/deployment"
	"github.com/linli/im/server/internal/tenancy"
)

func main() {
	config := flag.String("config", "", "absolute private platform configuration file")
	root := flag.String("output", "", "absolute pre-existing private bundle directory")
	id := flag.String("release", "", "immutable platform release ID")
	check := flag.String("check-public-url", "", "validate a client build origin only; no files or network")
	flag.Parse()
	if *check != "" {
		if *config != "" || *root != "" || *id != "" || flag.NArg() != 0 || tenancy.PublicPlatformURL(*check) != nil {
			fail()
		}
		fmt.Println("Production platform origin syntax valid; DNS/TLS reachability not checked")
		return
	}
	if !filepath.IsAbs(*config) || flag.NArg() != 0 {
		fail()
	}
	f, err := os.Open(*config)
	if err != nil {
		fail()
	}
	c, err := deployment.ReadPlatformConfig(f)
	f.Close()
	if err != nil {
		fail()
	}
	r, err := deployment.WritePlatformRelease(*root, c, *id)
	if err != nil {
		fail()
	}
	fmt.Printf("Platform bundle ready: release=%s schema=%d digest=%s; no deployment performed\n", r.ID, r.SchemaVersion, r.ComposeSHA256)
}
func fail() {
	fmt.Fprintln(os.Stderr, "Platform bundle not generated: check private configuration, identities, addresses, suppliers, pinned images and immutable output. No deployment performed.")
	os.Exit(1)
}
