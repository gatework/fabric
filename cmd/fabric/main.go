// Command fabric manages multi-region Cisco Nexus VXLAN EVPN fabrics.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/gatework/fabric/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}
