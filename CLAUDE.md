# docker-cleaner

One command that removes docker resources nothing will use again. Read the README first — it states the rules the code implements.

## Build and test

- `go-toolchain` bare in the repository root. Never a bare `go` command, and never pipe or redirect its output.
- `UPDATE_GOLDEN=1 go-toolchain` re-blesses the report goldens and `docs/cmdline_args.txt`.
- The `dats/` suite needs bubblewrap or docker for its sandbox; CI installs bubblewrap.

## Layout

- `main.go`, `cmd/` -- cobra surface. `root.go` is the cleanup, `watch.go` the events recorder, `inject.go` the runner seam, `docs.go` the generated help dump.
- `internal/dockercli/` -- every docker read and write. `read.go` gathers a Snapshot, `apply.go` turns one target into one argv.
- `internal/compose/` -- would a project still on disk attach this? `index.go` remembers paths, `scan.go` searches for them, `project.go` renders a compose file, `discover.go` decides alive, deleted or unknown.
- `internal/plan/` -- `Compute` is pure: snapshot plus options plus one clock reading gives the plan. Nothing else decides what goes.
- `internal/report/` -- the terminal report (`text.go`) and the JSON document (`json.go`).
- `internal/progress/` -- says what the run is doing, on stderr, while it does it. A nil `*Reporter` is a working no-op.
- `internal/run/` -- read, discover, decide, show, confirm, apply, report.
- `dats/cli.dats` -- end to end against `dats/fixtures/fake-docker`, a canned docker that logs every mutating call.

## Invariants

- A dry run issues no mutating command at all. The dats suite asserts the log file does not exist.
- Never `docker rm -v`, never `docker rmi -f`, never `docker image ls -a`. `--force` is right only on `buildx prune`, where it means "do not ask".
- A read that fails is fatal (exit 3): a partial read makes a confident, wrong plan. An apply failure is reported and the run continues (exit 1).
- "No compose file found" acts only when the search was exhaustive. Any unreadable directory or unwalkable mount keeps every unresolved project.
- The index is a cache of a fact, never the fact: a recorded path is `stat`ed before it is believed, and a damaged index is discarded rather than half-parsed.
- Zero `FinishedAt` parses to year one and beats every cutoff, so `dockercli.ParseTime` rejects it. See the trap list in the plan.
- `buildx prune` acts on one builder, so every builder from `buildx ls` gets its own command. Its `until=` takes a Go duration: `168h`, never `7d`.
- Protection is per image ID, not per tag: if one tag of an id is kept, no `rmi` is emitted for its other tags.
- Progress goes to stderr, never stdout. A `--json` document stays parseable while the run narrates itself.
- The compose search is bounded by `--scan-timeout` and by a depth limit, and it skips a directory it has already read, by device and inode. Each of those exits is a `Failure`, which marks the search incomplete and keeps every unresolved project.
- A file the render never reached is `Unreadable`, never a project that declares nothing. The second reading retires a live project.
