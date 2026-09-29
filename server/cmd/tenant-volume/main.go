// Fixed-path, networkless cold-volume helper. Only the trusted local backup
// driver mounts a named volume here; this is not a public command endpoint.
package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/linli/im/server/internal/backup"
)

func main() {
	var err error
	if runtime.GOOS != "linux" || len(os.Args) != 2 {
		err = backup.ErrInvalid
	} else {
		switch os.Args[1] {
		case "export":
			err = backup.ExportVolume("/volume", os.Stdout)
		case "import":
			err = backup.ReadVolume(os.Stdin, "/volume")
		default:
			err = backup.ErrInvalid
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cold volume operation unconfirmed")
		os.Exit(2)
	}
}
