package releasetests

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCSSBuildRespectsTMPDIRAndRemovesScratch(t *testing.T) {
	root := t.TempDir()
	scratch := filepath.Join(root, "configured scratch")
	static := filepath.Join(root, "internal", "web", "static")
	for _, dir := range []string{scratch, static} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	makefile, err := os.ReadFile(filepath.Join(repoRoot(t), "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	for file, body := range map[string][]byte{
		filepath.Join(root, "Makefile"):         makefile,
		filepath.Join(static, "styles.src.css"): []byte("/* test input */\n"),
	} {
		if err := os.WriteFile(file, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	probe := filepath.Join(root, "tailwind-probe")
	if err := os.WriteFile(probe, []byte(`#!/bin/sh
set -eu
output=
while [ "$#" -gt 0 ]; do
  if [ "$1" = -o ]; then shift; output=$1; fi
  shift
done
case "$output" in "$TMPDIR"/*|./internal/web/static/styles.css) ;; *) echo "output ignored TMPDIR: $output" >&2; exit 93;; esac
printf '/* same generated CSS */\n' > "$output"
`), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", scratch)
	// Skip the download target and execute only the actual CSS recipe against
	// a local compiler probe. No dependency installation or /tmp writes.
	cmd := exec.Command("make", "-o", "tools", "css", "TAILWIND_BIN="+probe)
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("CSS recipe ignored configured temporary storage: %v\n%s", err, output)
	}
	if files, err := os.ReadDir(scratch); err != nil || len(files) != 0 {
		t.Fatalf("CSS recipe retained scratch output: %v, %v", files, err)
	}
	if _, err := os.Stat(filepath.Join(static, ".brute-tw.src.css")); !os.IsNotExist(err) {
		t.Fatalf("CSS recipe retained its temporary source: %v", err)
	}
}
