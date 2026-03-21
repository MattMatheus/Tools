package store

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"artifactmount/internal/config"
)

type Store struct {
	cfg    config.Config
	mounts map[string]config.Mount
}

type Artifact struct {
	Meta    ArtifactMeta `json:"meta"`
	Content string       `json:"content"`
}

type ArtifactMeta struct {
	LogicalPath  string    `json:"logical_path"`
	PhysicalPath string    `json:"physical_path"`
	Mount        string    `json:"mount"`
	Kind         string    `json:"kind,omitempty"`
	Title        string    `json:"title,omitempty"`
	Stage        string    `json:"stage,omitempty"`
	CycleID      string    `json:"cycle_id,omitempty"`
	Status       string    `json:"status,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Ready        bool      `json:"ready"`
	Tags         []string  `json:"tags,omitempty"`
}

type SearchHit struct {
	LogicalPath string `json:"logical_path"`
	Line        int    `json:"line"`
	Snippet     string `json:"snippet"`
}

type Filter struct {
	Ready   *bool
	Stage   string
	CycleID string
	Kind    string
}

type WriteRequest struct {
	Body   string
	Kind   string
	Reason string
	Actor  string
}

type AppendRequest struct {
	Body   string
	Reason string
	Actor  string
}

type UpdateMetaRequest struct {
	Kind    *string
	Title   *string
	Stage   *string
	CycleID *string
	Status  *string
	Ready   *bool
	Tags    []string
	Reason  string
	Actor   string
}

type Manifest struct {
	RunID    string         `json:"run_id"`
	Purpose  string         `json:"purpose"`
	Selected []ManifestItem `json:"selected"`
}

type ManifestItem struct {
	Mount       string `json:"mount"`
	LogicalPath string `json:"logical_path"`
	Reason      string `json:"reason"`
}

type auditRecord struct {
	Timestamp   time.Time `json:"timestamp"`
	Operation   string    `json:"operation"`
	Mount       string    `json:"mount"`
	LogicalPath string    `json:"logical_path"`
	Actor       string    `json:"actor,omitempty"`
	Result      string    `json:"result"`
	Reason      string    `json:"reason,omitempty"`
}

func New(cfg config.Config) *Store {
	mounts := make(map[string]config.Mount, len(cfg.Mounts))
	for _, m := range cfg.Mounts {
		mounts[m.Name] = m
	}
	return &Store{cfg: cfg, mounts: mounts}
}

func (s *Store) Mounts() []config.Mount {
	out := make([]config.Mount, 0, len(s.mounts))
	for _, m := range s.mounts {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Store) Describe(_ context.Context, mount string) (config.Mount, error) {
	m, ok := s.mounts[mount]
	if !ok {
		return config.Mount{}, fmt.Errorf("unknown mount %q", mount)
	}
	return m, nil
}

func (s *Store) List(_ context.Context, logicalPath string) ([]ArtifactMeta, error) {
	return s.ListFiltered(context.Background(), logicalPath, Filter{})
}

func (s *Store) ListFiltered(_ context.Context, logicalPath string, filter Filter) ([]ArtifactMeta, error) {
	mount, fullPath, rel, err := s.resolveLogical(logicalPath)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		meta, err := s.buildMeta(mount, rel, fullPath, info)
		if err != nil {
			return nil, err
		}
		if !matchesFilter(meta, filter) {
			return []ArtifactMeta{}, nil
		}
		return []ArtifactMeta{meta}, nil
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return nil, err
	}
	out := make([]ArtifactMeta, 0, len(entries))
	for _, entry := range entries {
		p := filepath.Join(fullPath, entry.Name())
		i, err := entry.Info()
		if err != nil {
			return nil, err
		}
		childRel := filepath.ToSlash(filepath.Join(rel, entry.Name()))
		meta, err := s.buildMeta(mount, childRel, p, i)
		if err != nil {
			return nil, err
		}
		if !matchesFilter(meta, filter) {
			continue
		}
		out = append(out, meta)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LogicalPath < out[j].LogicalPath })
	return out, nil
}

func (s *Store) Read(_ context.Context, logicalPath string) (Artifact, error) {
	mount, fullPath, rel, err := s.resolveLogical(logicalPath)
	if err != nil {
		return Artifact{}, err
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return Artifact{}, err
	}
	if info.IsDir() {
		return Artifact{}, fmt.Errorf("cannot read directory %q", logicalPath)
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return Artifact{}, err
	}
	meta, err := s.buildMeta(mount, rel, fullPath, info)
	if err != nil {
		return Artifact{}, err
	}
	return Artifact{Meta: meta, Content: string(data)}, nil
}

func (s *Store) Write(_ context.Context, logicalPath string, req WriteRequest) (ArtifactMeta, error) {
	mount, fullPath, rel, err := s.resolveLogical(logicalPath)
	if err != nil {
		return ArtifactMeta{}, err
	}
	if mount.Mode != config.ReadWrite {
		return ArtifactMeta{}, fmt.Errorf("mount %q does not allow write", mount.Name)
	}
	if err := s.validateKind(mount, req.Kind); err != nil {
		return ArtifactMeta{}, err
	}
	if err := s.validateGlob(mount, rel); err != nil {
		return ArtifactMeta{}, err
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return ArtifactMeta{}, err
	}
	if err := os.WriteFile(fullPath, []byte(req.Body), 0o644); err != nil {
		return ArtifactMeta{}, err
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return ArtifactMeta{}, err
	}
	meta, err := s.buildMeta(mount, rel, fullPath, info)
	if err != nil {
		return ArtifactMeta{}, err
	}
	if meta.Kind == "" {
		meta.Kind = fallbackKind(mount, req.Kind)
	}
	if err := s.audit("write", mount.Name, meta.LogicalPath, req.Actor, "ok", req.Reason); err != nil {
		return ArtifactMeta{}, err
	}
	return meta, nil
}

func (s *Store) Append(_ context.Context, logicalPath string, req AppendRequest) (ArtifactMeta, error) {
	mount, fullPath, rel, err := s.resolveLogical(logicalPath)
	if err != nil {
		return ArtifactMeta{}, err
	}
	if mount.Mode != config.Append && mount.Mode != config.ReadWrite {
		return ArtifactMeta{}, fmt.Errorf("mount %q does not allow append", mount.Name)
	}
	if err := s.validateGlob(mount, rel); err != nil {
		return ArtifactMeta{}, err
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return ArtifactMeta{}, err
	}
	f, err := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return ArtifactMeta{}, err
	}
	defer f.Close()
	if _, err := io.WriteString(f, req.Body); err != nil {
		return ArtifactMeta{}, err
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return ArtifactMeta{}, err
	}
	meta, err := s.buildMeta(mount, rel, fullPath, info)
	if err != nil {
		return ArtifactMeta{}, err
	}
	if meta.Kind == "" {
		meta.Kind = mount.DefaultKind
	}
	if err := s.audit("append", mount.Name, meta.LogicalPath, req.Actor, "ok", req.Reason); err != nil {
		return ArtifactMeta{}, err
	}
	return meta, nil
}

func (s *Store) UpdateMeta(_ context.Context, logicalPath string, req UpdateMetaRequest) (ArtifactMeta, error) {
	mount, fullPath, rel, err := s.resolveLogical(logicalPath)
	if err != nil {
		return ArtifactMeta{}, err
	}
	if mount.Mode != config.ReadWrite {
		return ArtifactMeta{}, fmt.Errorf("mount %q does not allow metadata updates", mount.Name)
	}
	if filepath.Ext(fullPath) != ".md" {
		return ArtifactMeta{}, errors.New("metadata updates currently support markdown artifacts only")
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return ArtifactMeta{}, err
	}

	frontmatter, body := splitFrontmatter(string(data))
	fields := parseFrontmatterMap(frontmatter)
	if req.Kind != nil {
		fields["kind"] = *req.Kind
	}
	if req.Title != nil {
		fields["title"] = *req.Title
	}
	if req.Stage != nil {
		fields["stage"] = *req.Stage
	}
	if req.CycleID != nil {
		fields["cycle_id"] = *req.CycleID
	}
	if req.Status != nil {
		fields["status"] = *req.Status
	}
	if req.Ready != nil {
		if *req.Ready {
			fields["ready"] = "true"
		} else {
			fields["ready"] = "false"
		}
	}
	if req.Tags != nil {
		fields["tags"] = "[" + strings.Join(req.Tags, ", ") + "]"
	}

	newContent := renderFrontmatter(fields) + body
	if err := os.WriteFile(fullPath, []byte(newContent), 0o644); err != nil {
		return ArtifactMeta{}, err
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return ArtifactMeta{}, err
	}
	meta, err := s.buildMeta(mount, rel, fullPath, info)
	if err != nil {
		return ArtifactMeta{}, err
	}
	if err := s.audit("update-meta", mount.Name, meta.LogicalPath, req.Actor, "ok", req.Reason); err != nil {
		return ArtifactMeta{}, err
	}
	return meta, nil
}

func (s *Store) Search(_ context.Context, scope string, query string) ([]SearchHit, error) {
	if query == "" {
		return nil, errors.New("query is required")
	}

	root, mountName, err := s.searchRoot(scope)
	if err != nil {
		return nil, err
	}

	if _, err := exec.LookPath("rg"); err == nil {
		return s.searchWithRipgrep(root, mountName, query)
	}
	return s.searchWithScan(root, mountName, query)
}

func (s *Store) CreateManifest(_ context.Context, purpose string, items []ManifestItem) (Manifest, error) {
	if purpose == "" {
		return Manifest{}, errors.New("purpose is required")
	}
	for _, item := range items {
		_, fullPath, _, err := s.resolveLogical(item.LogicalPath)
		if err != nil {
			return Manifest{}, fmt.Errorf("manifest item %q: %w", item.LogicalPath, err)
		}
		if _, err := os.Stat(fullPath); err != nil {
			return Manifest{}, fmt.Errorf("manifest item %q: %w", item.LogicalPath, err)
		}
	}
	return Manifest{
		RunID:    time.Now().UTC().Format(time.RFC3339),
		Purpose:  purpose,
		Selected: items,
	}, nil
}

func (s *Store) SelectLatest(ctx context.Context, logicalPath string, filter Filter) (ArtifactMeta, error) {
	items, err := s.ListFiltered(ctx, logicalPath, filter)
	if err != nil {
		return ArtifactMeta{}, err
	}
	if len(items) == 0 {
		return ArtifactMeta{}, errors.New("no matching artifacts")
	}
	latest := items[0]
	for _, item := range items[1:] {
		if item.UpdatedAt.After(latest.UpdatedAt) {
			latest = item
		}
	}
	return latest, nil
}

func (s *Store) resolveLogical(logicalPath string) (config.Mount, string, string, error) {
	clean := filepath.ToSlash(filepath.Clean(logicalPath))
	if !strings.HasPrefix(clean, "/mounts/") {
		return config.Mount{}, "", "", fmt.Errorf("logical path must start with /mounts/: %q", logicalPath)
	}
	parts := strings.Split(strings.TrimPrefix(clean, "/mounts/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return config.Mount{}, "", "", errors.New("mount name is required")
	}
	mount, ok := s.mounts[parts[0]]
	if !ok {
		return config.Mount{}, "", "", fmt.Errorf("unknown mount %q", parts[0])
	}
	rel := "."
	if len(parts) > 1 {
		rel = filepath.Clean(filepath.Join(parts[1:]...))
	}
	fullPath := mount.Root
	if rel != "." {
		fullPath = filepath.Join(mount.Root, rel)
	}
	fullPath = filepath.Clean(fullPath)
	rootClean := filepath.Clean(mount.Root)
	if fullPath != rootClean && !strings.HasPrefix(fullPath, rootClean+string(os.PathSeparator)) {
		return config.Mount{}, "", "", fmt.Errorf("path escapes mount root: %q", logicalPath)
	}
	return mount, fullPath, rel, nil
}

func (s *Store) searchRoot(scope string) (string, string, error) {
	if scope == "" || scope == "all" {
		return s.cfg.BaseDir, "", nil
	}
	if strings.HasPrefix(scope, "/mounts/") {
		m, root, _, err := s.resolveLogical(scope)
		if err != nil {
			return "", "", err
		}
		return root, m.Name, nil
	}
	m, ok := s.mounts[scope]
	if !ok {
		return "", "", fmt.Errorf("unknown mount %q", scope)
	}
	return m.Root, m.Name, nil
}

func (s *Store) searchWithRipgrep(root string, mountName string, query string) ([]SearchHit, error) {
	cmd := exec.Command("rg", "-n", "--no-heading", query, root)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return []SearchHit{}, nil
		}
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	hits := make([]SearchHit, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}
		filePath := parts[0]
		lineNo := 0
		fmt.Sscanf(parts[1], "%d", &lineNo)
		logical := s.logicalForSearchPath(filePath, mountName)
		hits = append(hits, SearchHit{
			LogicalPath: logical,
			Line:        lineNo,
			Snippet:     parts[2],
		})
	}
	return hits, nil
}

func (s *Store) searchWithScan(root string, mountName string, query string) ([]SearchHit, error) {
	var hits []SearchHit
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			if strings.Contains(line, query) {
				hits = append(hits, SearchHit{
					LogicalPath: s.logicalForSearchPath(path, mountName),
					Line:        lineNo,
					Snippet:     line,
				})
			}
		}
		return nil
	})
	return hits, err
}

func (s *Store) logicalForSearchPath(path string, mountName string) string {
	path = filepath.Clean(path)
	if mountName != "" {
		mount := s.mounts[mountName]
		rel, _ := filepath.Rel(mount.Root, path)
		if rel == "." {
			return "/mounts/" + mountName
		}
		return "/mounts/" + mountName + "/" + filepath.ToSlash(rel)
	}
	for name, m := range s.mounts {
		root := filepath.Clean(m.Root)
		if path == root || strings.HasPrefix(path, root+string(os.PathSeparator)) {
			rel, _ := filepath.Rel(m.Root, path)
			if rel == "." {
				return "/mounts/" + name
			}
			return "/mounts/" + name + "/" + filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(path)
}

func (s *Store) buildMeta(mount config.Mount, rel string, fullPath string, info fs.FileInfo) (ArtifactMeta, error) {
	meta := ArtifactMeta{
		LogicalPath:  "/mounts/" + mount.Name,
		PhysicalPath: fullPath,
		Mount:        mount.Name,
		CreatedAt:    info.ModTime(),
		UpdatedAt:    info.ModTime(),
		Kind:         mount.DefaultKind,
	}
	if rel != "." {
		meta.LogicalPath += "/" + filepath.ToSlash(rel)
	}
	if info.IsDir() {
		meta.Kind = "directory"
		meta.Title = filepath.Base(fullPath)
		return meta, nil
	}
	meta.Title = filepath.Base(fullPath)

	data, err := os.ReadFile(fullPath)
	if err == nil {
		applyFrontmatter(&meta, string(data))
	}
	return meta, nil
}

func applyFrontmatter(meta *ArtifactMeta, body string) {
	block, _ := splitFrontmatter(body)
	if block == "" {
		return
	}
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		switch key {
		case "kind":
			meta.Kind = value
		case "title":
			meta.Title = value
		case "stage":
			meta.Stage = value
		case "cycle_id":
			meta.CycleID = value
		case "status":
			meta.Status = value
		case "ready":
			meta.Ready = strings.EqualFold(value, "true")
		case "tags":
			meta.Tags = parseList(value)
		}
	}
}

func splitFrontmatter(body string) (string, string) {
	if !strings.HasPrefix(body, "---\n") {
		return "", body
	}
	end := strings.Index(body[4:], "\n---")
	if end < 0 {
		return "", body
	}
	blockEnd := 4 + end
	frontmatter := body[4:blockEnd]
	remainder := strings.TrimPrefix(body[blockEnd:], "\n---")
	remainder = strings.TrimPrefix(remainder, "\n")
	return frontmatter, remainder
}

func parseFrontmatterMap(frontmatter string) map[string]string {
	fields := map[string]string{}
	for _, line := range strings.Split(frontmatter, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return fields
}

func renderFrontmatter(fields map[string]string) string {
	if len(fields) == 0 {
		return ""
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("---\n")
	for _, key := range keys {
		b.WriteString(key)
		b.WriteString(": ")
		b.WriteString(fields[key])
		b.WriteString("\n")
	}
	b.WriteString("---\n\n")
	return b.String()
}

func parseList(value string) []string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(strings.TrimSpace(part), `"'`)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func matchesFilter(meta ArtifactMeta, filter Filter) bool {
	if filter.Ready != nil && meta.Ready != *filter.Ready {
		return false
	}
	if filter.Stage != "" && meta.Stage != filter.Stage {
		return false
	}
	if filter.CycleID != "" && meta.CycleID != filter.CycleID {
		return false
	}
	if filter.Kind != "" && meta.Kind != filter.Kind {
		return false
	}
	return true
}

func (s *Store) validateKind(mount config.Mount, kind string) error {
	if kind == "" {
		return nil
	}
	if len(mount.AllowedKinds) == 0 {
		return nil
	}
	for _, allowed := range mount.AllowedKinds {
		if allowed == kind {
			return nil
		}
	}
	return fmt.Errorf("kind %q is not allowed in mount %q", kind, mount.Name)
}

func (s *Store) validateGlob(mount config.Mount, rel string) error {
	if len(mount.AllowedGlobs) == 0 || rel == "." {
		return nil
	}
	slashed := filepath.ToSlash(rel)
	for _, pattern := range mount.AllowedGlobs {
		ok, err := filepath.Match(pattern, filepath.Base(slashed))
		if err == nil && ok {
			return nil
		}
		ok, err = filepath.Match(pattern, slashed)
		if err == nil && ok {
			return nil
		}
	}
	return fmt.Errorf("path %q does not match allowed globs for mount %q", rel, mount.Name)
}

func fallbackKind(mount config.Mount, requested string) string {
	if requested != "" {
		return requested
	}
	return mount.DefaultKind
}

func (s *Store) audit(operation string, mount string, logicalPath string, actor string, result string, reason string) error {
	record := auditRecord{
		Timestamp:   time.Now().UTC(),
		Operation:   operation,
		Mount:       mount,
		LogicalPath: logicalPath,
		Actor:       actor,
		Result:      result,
		Reason:      reason,
	}
	if err := os.MkdirAll(filepath.Dir(s.cfg.AuditLog), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(s.cfg.AuditLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	return enc.Encode(record)
}
