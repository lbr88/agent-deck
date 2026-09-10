package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"al.essio.dev/pkg/shellescape"
)

// Rename updates only this row's intent; its root OMP extension applies it via
// the provider API. Never inject /rename keystrokes into an active composer.
func (i *Instance) syncOmpTitle(title string) error {
	data, err := json.Marshal(struct {
		Title string `json:"title"`
	}{title})
	if err != nil {
		return err
	}
	script := fmt.Sprintf(`dir=%s; if [ -L "$dir" ] || { [ -e "$dir" ] && [ ! -d "$dir" ]; }; then echo 'Invalid OMP session directory; history preserved' >&2; exit 1; fi; umask 077; mkdir -p "$dir" && printf '%%s' %s > "$dir/.agent-deck-title.json.$$" && mv -f "$dir/.agent-deck-title.json.$$" "$dir/.agent-deck-title.json"`,
		ompAgentDeckSessionDirExpr(i.ID), shellescape.Quote(string(data)))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if i.SSHHost != "" {
		_, err = (&SSHRunner{Host: i.SSHHost}).remoteExec(ctx, "bash -c "+shellescape.Quote(script), nil)
	} else if i.IsSandboxed() {
		if i.SandboxContainer == "" {
			return nil
		} // Start seeds intent from persisted title.
		// #nosec G204 -- fixed docker exec; container is a separate argv value,
		// and the generated script shell-quotes the row ID and JSON title.
		err = exec.CommandContext(ctx, "docker", "exec", i.SandboxContainer, "bash", "-c", script).Run()
	} else {
		err = exec.CommandContext(ctx, "bash", "-c", script).Run()
	}
	if err != nil {
		return fmt.Errorf("OMP title sync failed: %w", err)
	}
	return nil
}
