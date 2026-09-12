package tmux

import (
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/termreply"
)

func TestIndexSwitchKey_Disabled(t *testing.T) {
	// SwitchKeyByte 0 => disabled: nothing matches, even a Ctrl+S byte.
	if idx, in := indexSwitchKey([]byte("\x13"), AttachOptions{}); idx != -1 || in != SwitchNone {
		t.Fatalf("disabled switch key matched: (%d,%v)", idx, in)
	}
}

func TestIndexSwitchKey_RawByte(t *testing.T) {
	opts := AttachOptions{SwitchKeyByte: 0x13} // Ctrl+S
	if idx, in := indexSwitchKey([]byte("\x13"), opts); idx != 0 || in != SwitchRequested {
		t.Fatalf("raw Ctrl+S: got (%d,%v) want (0, next)", idx, in)
	}
	// Bytes before the switch key: index points at the key.
	if idx, in := indexSwitchKey([]byte("abc\x13"), opts); idx != 3 || in != SwitchRequested {
		t.Fatalf("prefixed Ctrl+S: got (%d,%v) want (3, next)", idx, in)
	}
}

func TestIndexSwitchKey_EncodedForms(t *testing.T) {
	opts := AttachOptions{SwitchKeyByte: 0x13} // Ctrl+S, keycode 's' = 115
	// kitty CSI-u form: ESC[115;5u
	if idx, in := indexSwitchKey([]byte("\x1b[115;5u"), opts); idx != 0 || in != SwitchRequested {
		t.Fatalf("Ctrl+S csi-u: got (%d,%v)", idx, in)
	}
	// xterm modifyOtherKeys form: ESC[27;5;115~
	if idx, in := indexSwitchKey([]byte("\x1b[27;5;115~"), opts); idx != 0 || in != SwitchRequested {
		t.Fatalf("Ctrl+S modifyOtherKeys: got (%d,%v)", idx, in)
	}
}

func TestIndexSwitchKey_PlainTabIgnored(t *testing.T) {
	// A plain Tab byte must never be treated as a switch key.
	opts := AttachOptions{SwitchKeyByte: 0x13}
	if idx, in := indexSwitchKey([]byte("\t"), opts); idx != -1 || in != SwitchNone {
		t.Fatalf("plain Tab treated as switch: (%d,%v)", idx, in)
	}
}

func TestIndexSwitchKey_EnhancedCtrlTabDirections(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  SwitchIntent
	}{
		{name: "CSI u next", input: "\x1b[9;5u", want: SwitchNextRequested},
		{name: "CSI u previous", input: "\x1b[9;6u", want: SwitchPreviousRequested},
		{name: "CSI u event next", input: "\x1b[9;5:1u", want: SwitchNextRequested},
		{name: "CSI u event previous", input: "\x1b[9;6:1u", want: SwitchPreviousRequested},
		{name: "modifyOtherKeys next", input: "\x1b[27;5;9~", want: SwitchNextFallbackRequested},
		{name: "modifyOtherKeys previous", input: "\x1b[27;6;9~", want: SwitchPreviousFallbackRequested},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, got := indexSwitchKey([]byte(tt.input), AttachOptions{})
			if idx != 0 || got != tt.want {
				t.Fatalf("indexSwitchKey(%q) = (%d, %v), want (0, %v)", tt.input, idx, got, tt.want)
			}
		})
	}
}

func TestIndexSwitchKey_OrdinaryInputDoesNotAllocate(t *testing.T) {
	input := []byte("ordinary attached input")
	if got := testing.AllocsPerRun(1000, func() {
		_, _ = indexSwitchKey(input, AttachOptions{})
	}); got != 0 {
		t.Fatalf("indexSwitchKey allocated %.1f times per ordinary input chunk, want 0", got)
	}
}

// The reply filter the attach loop runs over stdin must pass the switch key
// through unchanged (same guarantee the detach key relies on). Cover both the
// raw control byte and its CSI-u encoding.
func TestReplyFilterPreservesSwitchKey(t *testing.T) {
	for _, seq := range []string{"\x13", "\x1b[115;5u", "\x1b[9;5u", "\x1b[27;6;9~"} {
		var f termreply.Filter
		out := f.Consume([]byte(seq), true, false) // armed = stricter post-attach state
		if string(out) != seq {
			t.Fatalf("filter altered switch key %q -> %q", seq, out)
		}
	}
}
