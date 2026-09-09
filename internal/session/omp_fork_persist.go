package session

import "encoding/json"

const toolDataOmpPendingForkCommandKey = "omp_pending_fork_command"

// OMP keeps its native-fork recipe until the child acknowledges its identity.
// Persist that intent in the tool_data extras zone, without changing any other
// provider's transient fork handling or the positional SQLite tool-data schema.
func writeOmpPendingForkToToolData(td json.RawMessage, inst *Instance) json.RawMessage {
	inst.mu.RLock()
	if inst.Tool != "omp" {
		inst.mu.RUnlock()
		return td
	}
	command := ""
	if inst.IsForkAwaitingStart {
		command = inst.ForkStartCommand
	}
	inst.mu.RUnlock()

	var fields map[string]json.RawMessage
	_ = json.Unmarshal(td, &fields)
	if fields == nil {
		fields = make(map[string]json.RawMessage)
	}
	// Explicit empty is required: omitting an acknowledged recipe would let
	// MergeToolDataExtras restore the old pending value on the next save.
	fields[toolDataOmpPendingForkCommandKey], _ = json.Marshal(command)
	out, _ := json.Marshal(fields)
	return out
}

func readOmpPendingForkFromToolData(td json.RawMessage, tool string) string {
	if tool != "omp" {
		return ""
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(td, &fields); err != nil {
		return ""
	}
	var command string
	if err := json.Unmarshal(fields[toolDataOmpPendingForkCommandKey], &command); err != nil {
		return ""
	}
	return command
}
