package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type MountMode string

const (
	ReadOnly  MountMode = "readonly"
	Append    MountMode = "append"
	ReadWrite MountMode = "readwrite"
)

type Config struct {
	Version  int     `json:"version"`
	BaseDir  string  `json:"base_dir"`
	AuditLog string  `json:"audit_log"`
	Mounts   []Mount `json:"mounts"`
}

type Mount struct {
	Name                string    `json:"name"`
	Root                string    `json:"root"`
	Purpose             string    `json:"purpose"`
	Mode                MountMode `json:"mode"`
	DefaultKind         string    `json:"default_kind"`
	AllowedKinds        []string  `json:"allowed_kinds"`
	AllowedGlobs        []string  `json:"allowed_globs"`
	RequiresHumanReview bool      `json:"requires_human_review"`
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config JSON: %w", err)
	}

	if cfg.Version == 0 {
		cfg.Version = 1
	}

	configDir := filepath.Dir(path)
	if cfg.BaseDir == "" {
		cfg.BaseDir = configDir
	}
	if !filepath.IsAbs(cfg.BaseDir) {
		cfg.BaseDir = filepath.Clean(filepath.Join(configDir, cfg.BaseDir))
	}

	if cfg.AuditLog == "" {
		cfg.AuditLog = filepath.Join(cfg.BaseDir, ".artifact-audit.log")
	} else if !filepath.IsAbs(cfg.AuditLog) {
		cfg.AuditLog = filepath.Clean(filepath.Join(cfg.BaseDir, cfg.AuditLog))
	}

	seen := map[string]struct{}{}
	for i := range cfg.Mounts {
		m := &cfg.Mounts[i]
		if strings.TrimSpace(m.Name) == "" {
			return Config{}, fmt.Errorf("mount[%d] missing name", i)
		}
		if _, ok := seen[m.Name]; ok {
			return Config{}, fmt.Errorf("duplicate mount name %q", m.Name)
		}
		seen[m.Name] = struct{}{}

		if m.Root == "" {
			return Config{}, fmt.Errorf("mount %q missing root", m.Name)
		}
		if !filepath.IsAbs(m.Root) {
			m.Root = filepath.Clean(filepath.Join(cfg.BaseDir, m.Root))
		}

		switch m.Mode {
		case ReadOnly, Append, ReadWrite:
		default:
			return Config{}, fmt.Errorf("mount %q has invalid mode %q", m.Name, m.Mode)
		}
	}

	return cfg, nil
}
