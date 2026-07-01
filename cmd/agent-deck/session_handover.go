package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

type handoverSessionOptions struct {
	Source      string
	To          string
	Title       string
	GroupPath   string
	ProjectPath string
	Message     string
	Start       bool
}

type handoverSessionResult struct {
	Result  *session.HandoverResult
	Source  *session.Instance
	Target  *session.Instance
	Started bool
}

type handoverSessionDeps struct {
	load  func(string) (*session.Storage, []*session.Instance, []*session.GroupData, error)
	save  func(*session.Storage, []*session.Instance, []*session.GroupData) error
	start func(*session.Instance, string) error
}

func (d handoverSessionDeps) withDefaults() handoverSessionDeps {
	if d.load == nil {
		d.load = loadSessionData
	}
	if d.save == nil {
		d.save = saveSessionData
	}
	if d.start == nil {
		d.start = func(inst *session.Instance, prompt string) error {
			session.ScrubProcessEnvForChildLaunch(inst)
			if err := inst.StartWithMessage(prompt); err != nil {
				return err
			}
			inst.PostStartSync(3 * time.Second)
			return nil
		}
	}
	return d
}

func handoverSession(profile string, opts handoverSessionOptions, deps handoverSessionDeps) (*handoverSessionResult, error) {
	deps = deps.withDefaults()
	sourceID := strings.TrimSpace(opts.Source)
	if sourceID == "" {
		return nil, fmt.Errorf("source session is required")
	}
	if strings.TrimSpace(opts.To) == "" {
		return nil, fmt.Errorf("--to is required")
	}

	storage, instances, groups, err := deps.load(profile)
	if err != nil {
		return nil, err
	}

	source, errMsg, _ := ResolveSession(sourceID, instances)
	if source == nil {
		return nil, fmt.Errorf("%s", errMsg)
	}

	projectPath := strings.TrimSpace(opts.ProjectPath)
	if projectPath != "" {
		resolved, err := resolveAddPath(projectPath)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve project path: %w", err)
		}
		projectPath = resolved
	}

	groupPath := strings.TrimSpace(opts.GroupPath)
	if groupPath != "" {
		groupPath = normalizeGroupPath(groupPath)
	}

	result, err := session.HandoverSession(source, session.HandoverOptions{
		Target:      session.HandoverTarget(opts.To),
		Title:       opts.Title,
		GroupPath:   groupPath,
		ProjectPath: projectPath,
		Message:     opts.Message,
		Start:       opts.Start,
		Peers:       instances,
	})
	if err != nil {
		return nil, err
	}

	instances = append(instances, result.Target)
	if err := deps.save(storage, instances, groups); err != nil {
		return nil, fmt.Errorf("failed to save handed-over session: %w", err)
	}

	out := &handoverSessionResult{Result: result, Source: result.Source, Target: result.Target}
	if opts.Start {
		if err := deps.start(result.Target, result.HandoverPrompt); err != nil {
			return nil, fmt.Errorf("failed to start handed-over session: %w", err)
		}
		result.Started = true
		out.Started = true
		if err := deps.save(storage, instances, groups); err != nil {
			return nil, fmt.Errorf("failed to save started handed-over session: %w", err)
		}
	}
	return out, nil
}

func handleSessionHandover(profile string, args []string) {
	fs := flag.NewFlagSet("session handover", flag.ExitOnError)
	fs.SetOutput(os.Stdout)
	to := fs.String("to", "", "Target tool: claude, codex, or opencode")
	title := fs.String("title", "", "Title for the new session")
	titleShort := fs.String("t", "", "Title for the new session (short)")
	group := fs.String("group", "", "Group path for the new session")
	groupShort := fs.String("g", "", "Group path for the new session (short)")
	pathFlag := fs.String("path", "", "Project path for the new session")
	message := fs.String("message", "", "Operator instruction appended to the handover packet")
	messageShort := fs.String("m", "", "Operator instruction appended to the handover packet (short)")
	start := fs.Bool("start", false, "Start the handed-over session and send the handover packet")
	noStart := fs.Bool("no-start", false, "Create the handed-over session stopped")
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	quiet := fs.Bool("quiet", false, "Minimal output")
	quietShort := fs.Bool("q", false, "Minimal output (short)")

	fs.Usage = func() {
		fmt.Println("Usage: agent-deck session handover <source-session> --to <claude|codex|opencode> [options]")
		fmt.Println()
		fmt.Println("Create a new target-tool session with a deterministic handover packet.")
		fmt.Println()
		fmt.Println("Options:")
		fs.PrintDefaults()
		fmt.Println()
		fmt.Println("Examples:")
		fmt.Println("  agent-deck session handover api-worker --to codex")
		fmt.Println("  agent-deck session handover api-worker --to claude --start -m \"Continue with tests\"")
	}

	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		os.Exit(1)
	}

	out := NewCLIOutput(*jsonOutput, *quiet || *quietShort)
	if fs.NArg() != 1 {
		out.Error("usage: agent-deck session handover <source-session> --to <claude|codex|opencode>", ErrCodeInvalidOperation)
		os.Exit(1)
	}
	if *start && *noStart {
		out.Error("--start and --no-start cannot both be set", ErrCodeInvalidOperation)
		os.Exit(1)
	}

	result, err := handoverSession(profile, handoverSessionOptions{
		Source:      fs.Arg(0),
		To:          *to,
		Title:       mergeFlags(*title, *titleShort),
		GroupPath:   mergeFlags(*group, *groupShort),
		ProjectPath: *pathFlag,
		Message:     mergeFlags(*message, *messageShort),
		Start:       *start && !*noStart,
	}, handoverSessionDeps{})
	if err != nil {
		code := ErrCodeInvalidOperation
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "No session") {
			code = ErrCodeNotFound
		}
		out.Error(err.Error(), code)
		os.Exit(1)
	}

	out.Success(
		fmt.Sprintf("Handed over session: %s", result.Result.Target.Title),
		handoverResultJSON(result),
	)
}

func handoverResultJSON(result *handoverSessionResult) map[string]interface{} {
	res := result.Result
	target := res.Target
	return map[string]interface{}{
		"success":      true,
		"source_id":    res.Source.ID,
		"source_title": res.Source.Title,
		"source_tool":  res.Source.Tool,
		"target_id":    target.ID,
		"target_title": target.Title,
		"target_tool":  target.Tool,
		"command":      target.Command,
		"status":       string(target.Status),
		"group_path":   target.GroupPath,
		"project_path": target.ProjectPath,
		"started":      result.Started,
		"warning":      res.Warning,
	}
}
