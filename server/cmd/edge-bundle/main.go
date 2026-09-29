// Build operator-owned shared infrastructure without deploying or touching data.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/linli/im/server/internal/deployment"
	"github.com/linli/im/server/internal/privatefile"
)

func main() {
	config := flag.String("config", "", "absolute private configuration file")
	output := flag.String("out", "", "absolute new compose JSON file")
	flag.Parse()
	if flag.NArg() != 0 || run(*config, *output) != nil {
		fmt.Fprintln(os.Stderr, "Shared edge bundle failed; check private input and output paths.")
		os.Exit(1)
	}
}
func run(config, output string) error {
	if !filepath.IsAbs(config) || !filepath.IsAbs(output) {
		return deployment.ErrBundle
	}
	f, e := os.Open(config)
	if e != nil {
		return e
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 2097153))
	d.DisallowUnknownFields()
	var c deployment.EdgeConfig
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF {
		return deployment.ErrBundle
	}
	raw, e := deployment.BuildEdgeBundle(c)
	if e != nil {
		return e
	}
	w, e := privatefile.Create(output)
	if e != nil {
		return e
	}
	defer w.Close()
	if _, e = w.Write(raw); e != nil {
		return e
	}
	return w.Sync()
}
