package cli

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		os.Exit(m.Run())
	}
	defer stdin.Close()

	originalStdin := os.Stdin
	os.Stdin = stdin
	code := m.Run()
	os.Stdin = originalStdin
	os.Exit(code)
}
