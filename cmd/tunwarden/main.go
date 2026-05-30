package main

import (
	"context"
	"fmt"
	"os"

	"github.com/AidarKhusainov/tunwarden/internal/app"
)

func main() {
	if err := app.RunCLI(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "tunwarden:", err)
		os.Exit(1)
	}
}
