// Command afl locks files against modification using OS-level immutable flags.
package main

import (
	"os"

	"github.com/Mineru98/agent-file-lock/internal/cli"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	if version != "" {
		cli.Version = version
	}
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
