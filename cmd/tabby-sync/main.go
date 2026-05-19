// Command tabby-sync is the single-binary entry point. It is intentionally
// thin: all logic lives in internal/cli so it can be unit tested without
// invoking a subprocess.
package main

import (
	"context"
	"os"

	"github.com/kuddy-ai/tabby-sync/internal/cli"
)

func main() {
	os.Exit(cli.Run(context.Background(), os.Args, os.Getenv, os.Stdout, os.Stderr))
}
