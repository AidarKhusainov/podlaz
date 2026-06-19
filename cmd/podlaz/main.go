package main

import (
	"context"
	"fmt"
	"os"

	"github.com/AidarKhusainov/podlaz/internal/app/cli"
)

func main() {
	if err := cli.Run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "podlaz:", err)
		os.Exit(cli.ExitCode(err))
	}
}
