# Artifact Mount Prototype

Thin standalone prototype for a mount-based artifact interface.

Current scope:

- local filesystem-backed mounts
- JSON configuration
- logical namespace under `/mounts/<name>/...`
- `list`
- `select`
- `read`
- `update-meta`
- `write`
- `append`
- `search`
- `describe`
- `mounts list`
- `manifest create`
- audit log emission for mutating operations

This prototype is intentionally small and uses only the Go standard library.

## Build

```bash
go build ./cmd/artifacts
```

## Example

```bash
./artifacts --config ./configs/flywheel.sample.json mounts list
./artifacts --config ./configs/flywheel.sample.json describe planning
./artifacts --config ./configs/flywheel.sample.json list /mounts/planning
./artifacts --config ./configs/flywheel.sample.json list --ready true /mounts/planning
./artifacts --config ./configs/flywheel.sample.json select --ready true --stage planning /mounts/planning
./artifacts --config ./configs/default.sample.json mounts list
./artifacts --config ./configs/flywheel.sample.json search planning observer
```

## Mutation Example

Use a writable config based on `default.sample.json` or `flywheel.sample.json` for write and append operations.

```bash
./artifacts --config ./configs/default.sample.json write /mounts/planning/PLAN-demo.md --body "hello"
./artifacts --config ./configs/default.sample.json append /mounts/observer/events.log --body "event"
```

## Workflow Helpers

Use `select` when a stage needs the latest matching artifact rather than a manually chosen path.

```bash
./artifacts --config ./configs/flywheel.sample.json select --ready true --stage planning /mounts/planning
./artifacts --config ./configs/flywheel.sample.json select --ready true --stage cycle --kind observer_report /mounts/observer
```

Supported `list` and `select` filters:

- `--ready true|false`
- `--stage <stage>`
- `--cycle-id <id>`
- `--kind <kind>`

## State Transitions

Use `update-meta` to move an artifact forward without manually editing frontmatter.

Supported metadata updates:

- `--kind`
- `--title`
- `--stage`
- `--cycle-id`
- `--status`
- `--ready true|false`
- `--tags a,b,c`

Example:

```bash
./artifacts --config ./configs/default.sample.json update-meta \
  /mounts/planning/PLAN-demo.md \
  --status ready-for-architect \
  --reason 'workflow promotion test'
```

## Manifest Helpers

Manifests can be built from explicit paths or from selector expressions.

Selector format:

```text
<logical-path>[::reason][?ready=true&stage=planning&cycle_id=proto-001&kind=planning_note]
```

Examples:

```bash
./artifacts --config ./configs/flywheel.sample.json manifest create \
  --purpose architect-handoff \
  --select '/mounts/planning::planning-output?ready=true&stage=planning&kind=planning_note'

./artifacts --config ./configs/flywheel.sample.json manifest create \
  --purpose cycle-closure \
  --select '/mounts/planning::input-plan?ready=true&stage=planning' \
  --select '/mounts/observer::observer-output?ready=true&stage=cycle&kind=observer_report'
```
