package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestDataPlanePackageSelectionSupportsPrebuiltOrDevPackage(t *testing.T) {
	data, err := os.ReadFile("data-plane.sh")
	if err != nil {
		t.Fatalf("read data-plane script: %v", err)
	}
	script := string(data)
	for _, required := range []string{
		`: "${PODLAZ_E2E_PACKAGE_PATH:=}"`,
		`if [[ -n "${PODLAZ_E2E_PACKAGE_PATH}" ]]`,
		"configured data-plane package is missing",
		"configured data-plane package architecture mismatch",
		`dpkg-deb --field "${PODLAZ_E2E_PACKAGE_PATH}" Architecture`,
		`INSTALL_DEB="${PODLAZ_E2E_PACKAGE_PATH}"`,
		`bash scripts/build-deb.sh`,
		`INSTALL_DEB="${DEV_DEB}"`,
		`apt install -y "./${INSTALL_DEB}"`,
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("data-plane package selection lost %q", required)
		}
	}

	if strings.Contains(script, `apt install -y "./${DEV_DEB}"`) {
		t.Fatal("data-plane install must use the selected package instead of hard-coding the dev package")
	}
}
