package main

import (
	"context"
	"fmt"
	"os"

	"github.com/AidarKhusainov/podlaz/internal/app/daemon"
	daemonruntime "github.com/AidarKhusainov/podlaz/internal/daemon"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--nm-xray-restricted-exec" {
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "podlazd: invalid restricted child invocation")
			os.Exit(1)
		}
		if err := daemonruntime.RunNMOwnedRestrictedXrayChild(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, "podlazd: restricted Xray launch failed:", err)
			os.Exit(1)
		}
		return
	}
	if err := daemon.Run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "podlazd:", err)
		os.Exit(1)
	}
}
