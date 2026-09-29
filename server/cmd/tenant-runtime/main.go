// tenant-runtime is an image-internal entrypoint, not a remotely callable agent.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/linli/im/server/internal/deployment"
)

func run() error {
	if len(os.Args) == 3 && os.Args[1] == "health" {
		return deployment.RuntimeHealth(context.Background(), os.Args[2])
	}
	if len(os.Args) != 2 {
		return deployment.ErrBundle
	}
	role := os.Args[1]
	if role == "platform-gateway" {
		metadata, err := os.ReadFile("/opt/frogim/platform-web.json")
		if err != nil || deployment.ValidatePlatformWebMetadata(metadata, os.Getenv("FROGIM_PLATFORM_PUBLIC_URL")) != nil {
			return deployment.ErrBundle
		}
	}
	if role == "plugins" {
		if e := deployment.CheckPluginSeed("/plugins"); e != nil {
			return e
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		<-ctx.Done()
		return nil
	}
	if e := deployment.PrepareRuntime(role, "/config", os.Getenv(deployment.RuntimeFilesEnvironment)); e != nil {
		return e
	}
	if e := os.Unsetenv(deployment.RuntimeFilesEnvironment); e != nil {
		return e
	}
	command, args, e := deployment.RuntimeCommand(role)
	if e != nil {
		return e
	}
	return execute(command, args)
}
func main() {
	if run() != nil {
		// Never print file contents, child arguments or private environment.
		fmt.Fprintln(os.Stderr, "enterprise runtime initialization/health failed")
		os.Exit(1)
	}
}
