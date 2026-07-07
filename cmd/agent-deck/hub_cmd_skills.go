package main

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

//go:embed assets/skills/agent-deck-hub/*
var hubSkillFS embed.FS

var supportedHubSkills = map[string]struct{}{
	"agent-deck-hub": {},
}

func handleHubInstallSkill(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("install-skill requires exactly one argument: the skill name (e.g. agent-deck-hub)")
	}
	skillName := strings.TrimSpace(args[0])
	if _, ok := supportedHubSkills[skillName]; !ok {
		return fmt.Errorf("unsupported hub skill %q: only %v are available in this release", skillName, keys(supportedHubSkills))
	}

	poolDir, err := session.GetSkillPoolPath()
	if err != nil {
		return fmt.Errorf("resolve skill pool path: %w", err)
	}
	targetDir := filepath.Join(poolDir, skillName)
	for _, dir := range []string{filepath.Dir(poolDir), poolDir, targetDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}

	embedRoot := "assets/skills/" + skillName
	if err := fs.WalkDir(hubSkillFS, embedRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel := strings.TrimPrefix(path, embedRoot)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			return nil
		}
		dest := filepath.Join(targetDir, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(dest, 0o700)
		}
		data, err := fs.ReadFile(hubSkillFS, path)
		if err != nil {
			return fmt.Errorf("read embedded file %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return fmt.Errorf("create directory %s: %w", filepath.Dir(dest), err)
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", dest, err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("install skill files: %w", err)
	}

	fmt.Printf("Installed skill: %s -> %s\n", skillName, targetDir)
	return nil
}
