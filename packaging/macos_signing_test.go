//go:build darwin

package packaging_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the real agent, not a stand-in executable. This runs in the existing
// macOS PR test job without accessing release signing credentials.
func TestMacOSSigning(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "tart-guest-agent")
	//nolint:gosec // The output path is owned by this test.
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build agent: %v\n%s", err, output)
	}

	sign := func(entitlements bool) {
		t.Helper()
		args := []string{
			"--force", "--sign", "-", "--identifier", "tart-guest-agent",
			"--options", "runtime", "--timestamp=none",
		}
		if entitlements {
			args = append(args, "--entitlements", filepath.Join(root, "packaging", "macos-entitlements.plist"))
		}
		args = append(args, binary)
		if output, err := exec.CommandContext(t.Context(), "codesign", args...).CombinedOutput(); err != nil {
			t.Fatalf("sign agent: %v\n%s", err, output)
		}
	}
	verify := func() ([]byte, error) {
		//nolint:gosec // The script and binary paths are owned by the repository and this test.
		return exec.CommandContext(t.Context(), "sh",
			filepath.Join(root, "packaging", "verify-macos-signing.sh"), binary).CombinedOutput()
	}

	sign(false)
	output, err := verify()
	if err == nil || !strings.Contains(string(output), "did not load the injection probe") {
		t.Fatalf("expected the unentitled hardened agent to reject injection: %v\n%s", err, output)
	}
	sign(true)
	if output, err := verify(); err != nil {
		t.Fatalf("entitled hardened agent must load the probe: %v\n%s", err, output)
	}
}
