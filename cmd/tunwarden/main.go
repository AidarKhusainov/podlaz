package main

import (
	"context"
	"fmt"
	"os"

	"github.com/AidarKhusainov/tunwarden/internal/app/cli"
)

func main() {
	if err := cli.Run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "tunwarden:", err)
		os.Exit(cli.ExitCode(err))
	}
}
