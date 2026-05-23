package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"artifactmount/internal/config"
)

func TestCreateManifestArtifactWritesToManifestMount(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	planDir := filepath.Join(root, "planning")
	manifestDir := filepath.Join(root, "manifests")
	auditLog := filepath.Join(root, "audit.log")

	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planDir, "PLAN-demo.md"), []byte("plan"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New(config.Config{
		BaseDir:  root,
		AuditLog: auditLog,
		Mounts: []config.Mount{
			{
				Name:         "planning",
				Root:         planDir,
				Mode:         config.ReadWrite,
				DefaultKind:  "planning_note",
				AllowedKinds: []string{"planning_note"},
				AllowedGlobs: []string{"PLAN-*.md"},
			},
			{
				Name:         "manifests",
				Root:         manifestDir,
				Mode:         config.ReadWrite,
				DefaultKind:  "manifest",
				AllowedKinds: []string{"manifest"},
				AllowedGlobs: []string{"MANIFEST-*.json"},
			},
		},
	})

	result, err := s.CreateManifestArtifact(context.Background(), "Architect Handoff", []ManifestItem{
		{
			Mount:       "planning",
			LogicalPath: "/mounts/planning/PLAN-demo.md",
			Reason:      "planning-output",
		},
	}, "tester")
	if err != nil {
		t.Fatalf("CreateManifestArtifact returned error: %v", err)
	}

	if result.Meta.Mount != "manifests" {
		t.Fatalf("expected manifests mount, got %q", result.Meta.Mount)
	}
	if !strings.HasPrefix(result.Meta.LogicalPath, "/mounts/manifests/MANIFEST-architect-handoff-") {
		t.Fatalf("unexpected logical path %q", result.Meta.LogicalPath)
	}

	data, err := os.ReadFile(result.Meta.PhysicalPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}

	var persisted Manifest
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("manifest file is not valid JSON: %v", err)
	}

	if persisted.Purpose != "Architect Handoff" {
		t.Fatalf("expected purpose to round-trip, got %q", persisted.Purpose)
	}
	if len(persisted.Selected) != 1 || persisted.Selected[0].Reason != "planning-output" {
		t.Fatalf("unexpected selected items: %+v", persisted.Selected)
	}

	auditData, err := os.ReadFile(auditLog)
	if err != nil {
		t.Fatalf("ReadFile(audit) returned error: %v", err)
	}
	if !strings.Contains(string(auditData), "\"operation\":\"write\"") {
		t.Fatalf("expected audit log to include write operation, got %s", string(auditData))
	}
}

func TestSanitizePurpose(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"architect-handoff":  "architect-handoff",
		"Architect Handoff":  "architect-handoff",
		" cycle closure ":    "cycle-closure",
		"***":                "manifest",
		"qa/evidence bundle": "qa-evidence-bundle",
	}

	for input, want := range cases {
		if got := sanitizePurpose(input); got != want {
			t.Fatalf("sanitizePurpose(%q) = %q, want %q", input, got, want)
		}
	}
}
