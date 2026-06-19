package main

import (
	"context"
	"fmt"
	"os"

	"github.com/AidarKhusainov/podlaz/internal/app/daemon"
)

func main() {
	if err := daemon.Run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "podlazd:", err)
		os.Exit(1)
	}
}
