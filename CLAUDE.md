# docker-cleaner

One command that removes docker resources nothing will use again. Read the README first — it states the rules the code implements.

## Build and test

- `go-toolchain` bare in the repository root. Never a bare `go` command, and never pipe or redirect its output.
- `UPDATE_GOLDEN=1 go-toolchain` re-blesses the report goldens and `docs/cmdline_args.txt`.
- The `dats/` suite needs bubblewrap or docker for its sandbox. CI installs bubblewrap.

## Layout

- `main.go`, `cmd/` -- cobra surface. `root.go` is the cleanup, `watch.go` the events recorder, `inject.go` the runner seam, `docs.go` the generated help dump.
- `internal/dockercli/` -- every docker read and write. `read.go` gathers a Snapshot, `apply.go` turns one target into one argv, `summary.go` renders the usage table.
- `internal/compose/` -- will a project still on disk attach this? `index.go` remembers paths, `probe.go` looks beside the projects docker named, `project.go` renders a compose file, `discover.go` decides alive, deleted or unknown.
- `internal/plan/` -- `Compute` is pure: snapshot plus options plus one clock reading gives the plan. Nothing else decides what goes.
- `internal/report/` -- the terminal report (`text.go`) and the JSON document (`json.go`).
- `internal/progress/` -- says what the run is doing, on stderr, while it does it. A nil `*Reporter` is a working no-op.
- `internal/run/` -- read, discover, decide, show, confirm, apply, report.
- `dats/cli.dats` -- end to end against `dats/fixtures/fake-docker`, a canned docker that logs every mutating call.

## Invariants

- A dry run issues no mutating command at all. The dats suite asserts the log file does not exist.
- Never `docker rm -v`, never `docker rmi -f`, never `docker image ls -a`. `--force` is right only on `buildx prune`, where it means "do not ask".
- A read that fails is fatal (exit 3): a partial read makes a confident, wrong plan. An apply failure is reported and the run continues (exit 1).
- The disk is never crawled for compose files. Docker names them on every container, running or stopped, and the index keeps them after the containers go. A project neither explains is looked for beside the project directories docker did name, one directory read per parent.
- "No compose file found" retires a project only when docker named that file already. A project nothing has ever named a file for is unknown. It keeps everything it claims.
- The index is a cache of a fact, never the fact. A recorded path is `stat`ed before it is believed. A damaged index is discarded rather than half-parsed.
- Zero `FinishedAt` parses to year one and beats every cutoff, so `dockercli.ParseTime` rejects it. See the trap list in the plan.
- `buildx prune` acts on one builder, so every builder from `buildx ls` gets its own command. Its `until=` takes a Go duration: `168h`, never `7d`.
- Protection is per image ID, not per tag: if one tag of an id is kept, no `rmi` is emitted for its other tags.
- Progress goes to stderr, never stdout. A `--json` document stays parseable while the run narrates itself.
- The read lists before it inspects. That makes the step count real. The fraction is docker calls finished over docker calls to make. The line names the step that has waited longest, because that step holds the run up.
- `docker system df` is never called, in any form. It measures every volume in a pass that reports nothing until it ends. On a full host it outlasts `--timeout` and takes the run down with it. See `internal/dockercli/volumesize.go`.
- Sizes come from what the read already fetched. Images carry their own size, and `container inspect --size` fills the writable layer in. A volume is measured by a walk of its `Mountpoint`. Those walks run several at a time, and each one reports.
- No decision about what to remove reads a size. A volume nothing can read is reported as unmeasured. The plan is unaffected.
- The inspects run at the same time as each other, so the read costs the slowest call rather than the sum.
- FREED after an apply is the sizes the run already measured, never a second measurement.
- Every phase that calls docker is counted: the read, the apply, and the measurement after an apply. A call names itself before it runs, never after it returns. A slow removal is visible while it happens.
- `Stop` leaves the reporter usable, because the run still narrates after the report takes the screen. A later stage starts its draw loop again.
- The apply log writes through `Reporter.Log`. A drawn line carries no newline, so a write straight to stdout lands on the end of it.
- A parent directory the probe cannot read is a `Failure`, and the projects under it stay unresolved. The index record that retires a project is kept while a docker resource still claims it. A dry run and the apply after it therefore reach the same conclusion.
- A file the render never reached is `Unreadable`, never a project that declares nothing. The second reading retires a live project.
