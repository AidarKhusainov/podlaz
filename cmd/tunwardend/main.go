package main

import (
	"context"
	"fmt"
	"os"

	"github.com/AidarKhusainov/tunwarden/internal/app"
)

func main() {
	if err := app.RunDaemon(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "tunwardend:", err)
		os.Exit(1)
	}
}
