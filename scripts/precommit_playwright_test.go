package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreparePlaywrightBrowser_IgnoresDiscoveredSystemChromium(t *testing.T) {
	helper := precommitPlaywrightHelperPath(t)
	workDir := t.TempDir()
	binDir := filepath.Join(workDir, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create fake node_modules bin: %v", err)
	}

	marker := filepath.Join(workDir, "playwright-args")
	playwright := "#!/usr/bin/env bash\nprintf '%s\\n' \"$*\" > \"$PLAYWRIGHT_MARKER\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "playwright"), []byte(playwright), 0o755); err != nil {
		t.Fatalf("write fake playwright: %v", err)
	}
	// Reproduce this workstation: a system Chromium is discoverable, but it is
	// the confined Snap launcher and must not replace Playwright's matched build.
	if err := os.WriteFile(filepath.Join(binDir, "chromium"), []byte("#!/bin/sh\nexit 99\n"), 0o755); err != nil {
		t.Fatalf("write fake system chromium: %v", err)
	}

	cmd := exec.Command("bash", helper)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"PLAYWRIGHT_MARKER="+marker,
		"PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prepare Playwright browser: %v\n%s", err, out)
	}
	args, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("Playwright installer was not called: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(args)); got != "install chromium" {
		t.Fatalf("Playwright args = %q, want %q", got, "install chromium")
	}
}

func TestPreparePlaywrightBrowser_HonorsExplicitExecutable(t *testing.T) {
	helper := precommitPlaywrightHelperPath(t)
	workDir := t.TempDir()
	marker := filepath.Join(workDir, "playwright-args")

	cmd := exec.Command("bash", helper)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"PLAYWRIGHT_MARKER="+marker,
		"PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/opt/chrome-for-testing/chrome",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prepare explicit Playwright browser: %v\n%s", err, out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("Playwright installer ran despite explicit executable; stat error: %v", err)
	}
	if !strings.Contains(string(out), "/opt/chrome-for-testing/chrome") {
		t.Fatalf("output does not identify explicit executable:\n%s", out)
	}
}

func TestPlaywrightChromiumPath_UsesPlaywrightManagedBrowser(t *testing.T) {
	helper := precommitPlaywrightHelperPath(t)
	workDir := t.TempDir()
	binDir := filepath.Join(workDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create fake bin: %v", err)
	}

	want := filepath.Join(workDir, "ms-playwright", "chromium-1217", "chrome-linux64", "chrome")
	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		t.Fatalf("create fake Playwright browser directory: %v", err)
	}
	if err := os.WriteFile(want, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake Playwright browser: %v", err)
	}
	fakeNode := "#!/usr/bin/env bash\nprintf '%s' \"$PLAYWRIGHT_EXPECTED_CHROME\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "node"), []byte(fakeNode), 0o755); err != nil {
		t.Fatalf("write fake node: %v", err)
	}

	cmd := exec.Command("bash", "-c", "source \"$1\"; playwright_chromium_path", "bash", helper)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"PLAYWRIGHT_EXPECTED_CHROME="+want,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("resolve Playwright Chromium path: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != want {
		t.Fatalf("Playwright Chromium path = %q, want %q", got, want)
	}
}

func TestPlaywrightChromiumPath_HonorsExplicitExecutable(t *testing.T) {
	helper := precommitPlaywrightHelperPath(t)
	workDir := t.TempDir()
	binDir := filepath.Join(workDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create fake bin: %v", err)
	}

	want := filepath.Join(workDir, "chrome-for-testing")
	if err := os.WriteFile(want, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write explicit Chromium: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "node"), []byte("#!/bin/sh\nexit 99\n"), 0o755); err != nil {
		t.Fatalf("write rejecting node: %v", err)
	}

	cmd := exec.Command("bash", "-c", "source \"$1\"; playwright_chromium_path", "bash", helper)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH="+want,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("resolve explicit Playwright Chromium path: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != want {
		t.Fatalf("Playwright Chromium path = %q, want explicit path %q", got, want)
	}
}

func precommitPlaywrightHelperPath(t *testing.T) string {
	t.Helper()
	workDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	return filepath.Join(workDir, "prepare-playwright-browser.sh")
}
