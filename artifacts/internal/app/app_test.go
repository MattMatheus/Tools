package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"artifactmount/internal/config"
	"artifactmount/internal/store"
)

func TestResolveSelectorSelectsLatestMatchingArtifact(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	planDir := filepath.Join(root, "planning")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}

	olderPath := filepath.Join(planDir, "PLAN-old.md")
	newerPath := filepath.Join(planDir, "PLAN-new.md")

	olderBody := "---\nkind: planning_note\nready: true\nstage: planning\ntitle: Old\n---\nold\n"
	newerBody := "---\nkind: planning_note\nready: true\nstage: planning\ntitle: New\n---\nnew\n"

	if err := os.WriteFile(olderPath, []byte(olderBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newerPath, []byte(newerBody), 0o644); err != nil {
		t.Fatal(err)
	}

	oldTime := mustTime(t, "2026-03-21T18:00:00Z")
	newTime := mustTime(t, "2026-03-21T19:00:00Z")
	if err := os.Chtimes(olderPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newerPath, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	s := store.New(config.Config{
		BaseDir: root,
		Mounts: []config.Mount{
			{
				Name:         "planning",
				Root:         planDir,
				Mode:         config.ReadWrite,
				DefaultKind:  "planning_note",
				AllowedKinds: []string{"planning_note"},
				AllowedGlobs: []string{"PLAN-*.md"},
			},
		},
	})

	item, err := resolveSelector(context.Background(), s, "/mounts/planning::planning-output?ready=true&stage=planning&kind=planning_note")
	if err != nil {
		t.Fatalf("resolveSelector returned error: %v", err)
	}

	if item.LogicalPath != "/mounts/planning/PLAN-new.md" {
		t.Fatalf("expected latest artifact, got %q", item.LogicalPath)
	}
	if item.Reason != "planning-output" {
		t.Fatalf("expected explicit reason, got %q", item.Reason)
	}
}

func TestRunManifestCreatePersistsArtifact(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	planDir := filepath.Join(root, "planning")
	manifestDir := filepath.Join(root, "manifests")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}

	planPath := filepath.Join(planDir, "PLAN-demo.md")
	planBody := "---\nkind: planning_note\nready: true\nstage: planning\ntitle: Demo\n---\nbody\n"
	if err := os.WriteFile(planPath, []byte(planBody), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(root, "config.json")
	cfgBody := `{
  "version": 1,
  "base_dir": "` + root + `",
  "audit_log": "` + filepath.Join(root, "audit.log") + `",
  "mounts": [
    {
      "name": "planning",
      "root": "` + planDir + `",
      "mode": "readwrite",
      "default_kind": "planning_note",
      "allowed_kinds": ["planning_note"],
      "allowed_globs": ["PLAN-*.md"]
    },
    {
      "name": "manifests",
      "root": "` + manifestDir + `",
      "mode": "readwrite",
      "default_kind": "manifest",
      "allowed_kinds": ["manifest"],
      "allowed_globs": ["MANIFEST-*.json"]
    }
  ]
}`
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o644); err != nil {
		t.Fatal(err)
	}

	out := &testBuffer{}
	errOut := &testBuffer{}
	exitCode, err := Run(context.Background(), cfgPath, []string{
		"manifest",
		"create",
		"--purpose", "architect-handoff",
		"--select", "/mounts/planning::planning-output?ready=true&stage=planning&kind=planning_note",
	}, out, errOut)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("Run exitCode = %d, stderr = %q", exitCode, errOut.String())
	}

	var stdout map[string]any
	if err := json.Unmarshal(out.Bytes(), &stdout); err != nil {
		t.Fatalf("manifest output is not JSON: %v", err)
	}

	files, err := os.ReadDir(manifestDir)
	if err != nil {
		t.Fatalf("ReadDir returned error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected one manifest file, got %d", len(files))
	}
}

type testBuffer struct {
	data []byte
}

func (b *testBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *testBuffer) Bytes() []byte {
	return b.data
}

func (b *testBuffer) String() string {
	return string(b.data)
}

func mustTime(t *testing.T, raw string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatalf("time.Parse(%q) returned error: %v", raw, err)
	}
	return parsed
}
