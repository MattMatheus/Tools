package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"artifactmount/internal/config"
	"artifactmount/internal/store"
)

func Run(ctx context.Context, configPath string, args []string, stdout io.Writer, stderr io.Writer) (int, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return 1, err
	}
	s := store.New(cfg)

	if len(args) == 0 {
		printUsage(stdout)
		return 2, nil
	}

	switch args[0] {
	case "mounts":
		return runMounts(ctx, s, args[1:], stdout)
	case "describe":
		return runDescribe(ctx, s, args[1:], stdout)
	case "list":
		return runList(ctx, s, args[1:], stdout)
	case "select":
		return runSelect(ctx, s, args[1:], stdout, stderr)
	case "read":
		return runRead(ctx, s, args[1:], stdout)
	case "update-meta":
		return runUpdateMeta(ctx, s, args[1:], stdout, stderr)
	case "write":
		return runWrite(ctx, s, args[1:], stdout, stderr)
	case "append":
		return runAppend(ctx, s, args[1:], stdout, stderr)
	case "search":
		return runSearch(ctx, s, args[1:], stdout)
	case "manifest":
		return runManifest(ctx, s, args[1:], stdout)
	case "help":
		printUsage(stdout)
		return 0, nil
	default:
		return 2, fmt.Errorf("unknown command %q", args[0])
	}
}

func runMounts(ctx context.Context, s *store.Store, args []string, stdout io.Writer) (int, error) {
	if len(args) != 1 || args[0] != "list" {
		return 2, errors.New("usage: mounts list")
	}
	return writeJSON(stdout, s.Mounts())
}

func runDescribe(ctx context.Context, s *store.Store, args []string, stdout io.Writer) (int, error) {
	if len(args) != 1 {
		return 2, errors.New("usage: describe <mount>")
	}
	m, err := s.Describe(ctx, args[0])
	if err != nil {
		return 1, err
	}
	return writeJSON(stdout, m)
}

func runList(ctx context.Context, s *store.Store, args []string, stdout io.Writer) (int, error) {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	ready := fs.String("ready", "", "filter ready=true|false")
	stage := fs.String("stage", "", "filter by stage")
	cycleID := fs.String("cycle-id", "", "filter by cycle id")
	kind := fs.String("kind", "", "filter by artifact kind")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	if fs.NArg() != 1 {
		return 2, errors.New("usage: list [--ready true|false] [--stage name] [--cycle-id id] [--kind kind] <logical-path>")
	}
	filter, err := buildFilter(*ready, *stage, *cycleID, *kind)
	if err != nil {
		return 2, err
	}
	items, err := s.ListFiltered(ctx, fs.Arg(0), filter)
	if err != nil {
		return 1, err
	}
	return writeJSON(stdout, items)
}

func runSelect(ctx context.Context, s *store.Store, args []string, stdout io.Writer, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("select", flag.ContinueOnError)
	fs.SetOutput(stderr)
	mode := fs.String("mode", "latest", "selection mode")
	ready := fs.String("ready", "", "filter ready=true|false")
	stage := fs.String("stage", "", "filter by stage")
	cycleID := fs.String("cycle-id", "", "filter by cycle id")
	kind := fs.String("kind", "", "filter by artifact kind")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	if fs.NArg() != 1 {
		return 2, errors.New("usage: select [--mode latest] [--ready true|false] [--stage name] [--cycle-id id] [--kind kind] <logical-path>")
	}
	if *mode != "latest" {
		return 2, fmt.Errorf("unsupported select mode %q", *mode)
	}
	filter, err := buildFilter(*ready, *stage, *cycleID, *kind)
	if err != nil {
		return 2, err
	}
	item, err := s.SelectLatest(ctx, fs.Arg(0), filter)
	if err != nil {
		return 1, err
	}
	return writeJSON(stdout, item)
}

func runRead(ctx context.Context, s *store.Store, args []string, stdout io.Writer) (int, error) {
	if len(args) != 1 {
		return 2, errors.New("usage: read <logical-path>")
	}
	item, err := s.Read(ctx, args[0])
	if err != nil {
		return 1, err
	}
	return writeJSON(stdout, item)
}

func runUpdateMeta(ctx context.Context, s *store.Store, args []string, stdout io.Writer, stderr io.Writer) (int, error) {
	args = normalizePathFirstArgs(args)
	fs := flag.NewFlagSet("update-meta", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kind := fs.String("kind", "", "set kind")
	title := fs.String("title", "", "set title")
	stage := fs.String("stage", "", "set stage")
	cycleID := fs.String("cycle-id", "", "set cycle id")
	status := fs.String("status", "", "set status")
	ready := fs.String("ready", "", "set ready=true|false")
	tags := fs.String("tags", "", "set tags as comma-separated list")
	reason := fs.String("reason", "", "reason for metadata update")
	actor := fs.String("actor", "", "actor id")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	if fs.NArg() != 1 {
		return 2, errors.New("usage: update-meta <logical-path> [--kind kind] [--title title] [--stage stage] [--cycle-id id] [--status status] [--ready true|false] [--tags a,b] [--reason why]")
	}
	req := store.UpdateMetaRequest{
		Reason: *reason,
		Actor:  *actor,
	}
	if *kind != "" {
		req.Kind = kind
	}
	if *title != "" {
		req.Title = title
	}
	if *stage != "" {
		req.Stage = stage
	}
	if *cycleID != "" {
		req.CycleID = cycleID
	}
	if *status != "" {
		req.Status = status
	}
	if *ready != "" {
		switch strings.ToLower(*ready) {
		case "true":
			v := true
			req.Ready = &v
		case "false":
			v := false
			req.Ready = &v
		default:
			return 2, fmt.Errorf("--ready must be true or false, got %q", *ready)
		}
	}
	if *tags != "" {
		req.Tags = splitCSV(*tags)
	}
	meta, err := s.UpdateMeta(ctx, fs.Arg(0), req)
	if err != nil {
		return 1, err
	}
	return writeJSON(stdout, meta)
}

func runWrite(ctx context.Context, s *store.Store, args []string, stdout io.Writer, stderr io.Writer) (int, error) {
	args = normalizePathFirstArgs(args)
	fs := flag.NewFlagSet("write", flag.ContinueOnError)
	fs.SetOutput(stderr)
	from := fs.String("from", "", "read body from file")
	body := fs.String("body", "", "inline body")
	kind := fs.String("kind", "", "artifact kind")
	reason := fs.String("reason", "", "reason for write")
	actor := fs.String("actor", "", "actor id")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	if fs.NArg() != 1 {
		return 2, errors.New("usage: write <logical-path> [--from file | --body text]")
	}
	content, err := readBody(*from, *body)
	if err != nil {
		return 2, err
	}
	meta, err := s.Write(ctx, fs.Arg(0), store.WriteRequest{
		Body:   content,
		Kind:   *kind,
		Reason: *reason,
		Actor:  *actor,
	})
	if err != nil {
		return 1, err
	}
	return writeJSON(stdout, meta)
}

func runAppend(ctx context.Context, s *store.Store, args []string, stdout io.Writer, stderr io.Writer) (int, error) {
	args = normalizePathFirstArgs(args)
	fs := flag.NewFlagSet("append", flag.ContinueOnError)
	fs.SetOutput(stderr)
	from := fs.String("from", "", "read body from file")
	body := fs.String("body", "", "inline body")
	reason := fs.String("reason", "", "reason for append")
	actor := fs.String("actor", "", "actor id")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	if fs.NArg() != 1 {
		return 2, errors.New("usage: append <logical-path> [--from file | --body text]")
	}
	content, err := readBody(*from, *body)
	if err != nil {
		return 2, err
	}
	meta, err := s.Append(ctx, fs.Arg(0), store.AppendRequest{
		Body:   content,
		Reason: *reason,
		Actor:  *actor,
	})
	if err != nil {
		return 1, err
	}
	return writeJSON(stdout, meta)
}

func runSearch(ctx context.Context, s *store.Store, args []string, stdout io.Writer) (int, error) {
	if len(args) != 2 {
		return 2, errors.New("usage: search <scope|all> <query>")
	}
	hits, err := s.Search(ctx, args[0], args[1])
	if err != nil {
		return 1, err
	}
	return writeJSON(stdout, hits)
}

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}

func runManifest(ctx context.Context, s *store.Store, args []string, stdout io.Writer) (int, error) {
	if len(args) == 0 || args[0] != "create" {
		return 2, errors.New("usage: manifest create --purpose <purpose> [--from <logical-path>[::reason]] [--select <selector>]")
	}
	fs := flag.NewFlagSet("manifest create", flag.ContinueOnError)
	var from multiFlag
	var selectArgs multiFlag
	purpose := fs.String("purpose", "", "manifest purpose")
	actor := fs.String("actor", "", "actor id")
	fs.Var(&from, "from", "artifact logical path with optional ::reason")
	fs.Var(&selectArgs, "select", "selector expression: <logical-path>[::reason][?ready=true&stage=planning&cycle_id=proto-001&kind=planning_note]")
	if err := fs.Parse(args[1:]); err != nil {
		return 2, err
	}
	items := make([]store.ManifestItem, 0, len(from))
	for _, value := range from {
		path, reason, _ := strings.Cut(value, "::")
		path = strings.TrimSpace(path)
		reason = strings.TrimSpace(reason)
		items = append(items, store.ManifestItem{
			Mount:       mountFromLogical(path),
			LogicalPath: path,
			Reason:      reason,
		})
	}
	for _, value := range selectArgs {
		item, err := resolveSelector(ctx, s, value)
		if err != nil {
			return 1, err
		}
		items = append(items, item)
	}
	manifest, err := s.CreateManifestArtifact(ctx, *purpose, items, *actor)
	if err != nil {
		return 1, err
	}
	return writeJSON(stdout, manifest)
}

func mountFromLogical(path string) string {
	path = strings.TrimPrefix(path, "/mounts/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func readBody(from string, body string) (string, error) {
	switch {
	case from != "" && body != "":
		return "", errors.New("use only one of --from or --body")
	case from != "":
		data, err := os.ReadFile(from)
		if err != nil {
			return "", err
		}
		return string(data), nil
	case body != "":
		return body, nil
	default:
		return "", errors.New("one of --from or --body is required")
	}
}

func normalizePathFirstArgs(args []string) []string {
	if len(args) == 0 {
		return args
	}
	if strings.HasPrefix(args[0], "-") {
		return args
	}
	return append(args[1:], args[0])
}

func writeJSON(w io.Writer, v any) (int, error) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return 1, err
	}
	return 0, nil
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage:")
	fmt.Fprintln(w, "  artifacts --config <config.json> mounts list")
	fmt.Fprintln(w, "  artifacts --config <config.json> describe <mount>")
	fmt.Fprintln(w, "  artifacts --config <config.json> list [--ready true|false] [--stage name] [--cycle-id id] [--kind kind] <logical-path>")
	fmt.Fprintln(w, "  artifacts --config <config.json> select [--mode latest] [--ready true|false] [--stage name] [--cycle-id id] [--kind kind] <logical-path>")
	fmt.Fprintln(w, "  artifacts --config <config.json> read <logical-path>")
	fmt.Fprintln(w, "  artifacts --config <config.json> update-meta <logical-path> [--status status] [--ready true|false] [...]")
	fmt.Fprintln(w, "  artifacts --config <config.json> write <logical-path> [--from file | --body text]")
	fmt.Fprintln(w, "  artifacts --config <config.json> append <logical-path> [--from file | --body text]")
	fmt.Fprintln(w, "  artifacts --config <config.json> search <scope|all> <query>")
	fmt.Fprintln(w, "  artifacts --config <config.json> manifest create --purpose <purpose> [--actor id] [--from <logical-path>[::reason]] [--select <selector>]")
}

func buildFilter(ready string, stage string, cycleID string, kind string) (store.Filter, error) {
	filter := store.Filter{
		Stage:   stage,
		CycleID: cycleID,
		Kind:    kind,
	}
	if ready != "" {
		switch strings.ToLower(ready) {
		case "true":
			v := true
			filter.Ready = &v
		case "false":
			v := false
			filter.Ready = &v
		default:
			return store.Filter{}, fmt.Errorf("--ready must be true or false, got %q", ready)
		}
	}
	return filter, nil
}

func resolveSelector(ctx context.Context, s *store.Store, raw string) (store.ManifestItem, error) {
	pathAndReason, query, _ := strings.Cut(raw, "?")
	path, reason, _ := strings.Cut(pathAndReason, "::")
	path = strings.TrimSpace(path)
	reason = strings.TrimSpace(reason)
	params, err := parseSelectorQuery(query)
	if err != nil {
		return store.ManifestItem{}, err
	}
	filter, err := buildFilter(params["ready"], params["stage"], params["cycle_id"], params["kind"])
	if err != nil {
		return store.ManifestItem{}, err
	}
	meta, err := s.SelectLatest(ctx, path, filter)
	if err != nil {
		return store.ManifestItem{}, fmt.Errorf("select %q: %w", raw, err)
	}
	if reason == "" {
		reason = "selected"
	}
	return store.ManifestItem{
		Mount:       meta.Mount,
		LogicalPath: meta.LogicalPath,
		Reason:      reason,
	}, nil
}

func parseSelectorQuery(raw string) (map[string]string, error) {
	out := map[string]string{}
	if raw == "" {
		return out, nil
	}
	for _, part := range strings.Split(raw, "&") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("invalid selector query component %q", part)
		}
		out[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return out, nil
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
