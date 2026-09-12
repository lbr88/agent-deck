package main

import (
	"bytes"
	"errors"
	"testing"
)

func TestRunWithKeyboardCleanup_DisablesProtocolsAfterMainScreenRestore(t *testing.T) {
	var output bytes.Buffer
	runErr := errors.New("program stopped")

	err := runWithKeyboardCleanup(func() error {
		_, _ = output.WriteString("\x1b[?1049l")
		return runErr
	}, &output)

	if !errors.Is(err, runErr) {
		t.Fatalf("runWithKeyboardCleanup error = %v, want %v", err, runErr)
	}
	mainScreenIndex := bytes.LastIndex(output.Bytes(), []byte("\x1b[?1049l"))
	disableIndex := bytes.LastIndex(output.Bytes(), []byte("\x1b[>4;0m\x1b[<u"))
	if mainScreenIndex < 0 || disableIndex <= mainScreenIndex {
		t.Fatalf("keyboard cleanup must occur after main-screen restore, output %q", output.String())
	}
}
