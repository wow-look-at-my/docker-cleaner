# docker-cleaner

One command that removes the docker resources nothing will use again, and shows you the list before it touches anything.

```sh
docker-cleaner --dry-run   # print the plan, remove nothing
docker-cleaner             # print the plan, then ask
docker-cleaner --yes       # print the plan and apply it
```

There is nothing to configure. Every read and every delete is a stock `docker` invocation.

## What it removes

- Containers that have been stopped longer than `--age` (default `30d`), plus containers created that never started. Removing one reclaims its writable layer, which no other command reaches.
- Images no container references, **except** the newest of each repository and anything tagged `latest`. Repository is the reference minus everything after the last colon, so `localhost:5000/app:v1` is repository `localhost:5000/app`.
- Volumes and custom networks nothing will attach, named and anonymous alike.
- Build cache nothing has used in `--build-cache-age` (default `7d`) — on every builder, not just the configured one.

Removing a stale container frees its image, volume and network in the same run. Each of those lines says which container freed it.

## What it never removes

Bind mounts (docker cannot), anything a surviving container holds, and the newest image of any repository. It also keeps anything a compose project still on disk attaches on its next `up`. Nothing else is exempt.

## A stack that is down is not a stack that is deleted

`docker compose down` deletes the containers, so the project disappears from docker's view and its named volumes look like garbage. They are not. docker-cleaner treats a project as alive while its compose file exists on disk, so a downed stack keeps its database. **Deleting the compose file is what retires a project** — that is the whole escape hatch, and it needs no flag.

Finding those files is fast because the tool remembers them: every container names its project's compose files in a label, and that goes into an index at `/var/lib/docker-cleaner/projects.json`. An ordinary run is index lookups and `stat` calls. Only a project the index has never heard of costs a filesystem search, and the results go into the index, so it happens once per project rather than once per run.

If the search cannot run everywhere — an unreadable directory, a denied mount — the run says so and keeps every unresolved project's resources. Not looking is never evidence of deletion. Run it as root for a complete search.

## While it works

A run reports each step on stderr. It names the docker read it waits on. It counts the directories the search walks and the compose files it reads. On a terminal it redraws a line in place. Anywhere else it prints a line per change, so a log keeps every step. `--progress never` turns it off. Only the report goes to stdout, so `--json` stays parseable either way.

The search gives up after `--scan-timeout` (default `5m`). Before this it waited forever on a wedged network mount, or on a directory that leads back into itself. To give up is safe: the run then reports the search as incomplete, which keeps every project it cannot resolve. `Ctrl-C` stops a run at once.

### Optional: `docker-cleaner watch`

Docker forgets a project's file paths when its containers go, so a stack brought up and down between two cleanups leaves nothing to read. `docker-cleaner watch` tails `docker events` and records those paths as containers are created, which keeps such a project off the search path. It is an optimisation, never a requirement: a project it misses is simply searched for.

```ini
# /etc/systemd/system/docker-cleaner-watch.service
[Unit]
After=docker.service
Requires=docker.service
[Service]
ExecStart=/usr/local/bin/docker-cleaner watch
Restart=always
[Install]
WantedBy=multi-user.target
```

## Speed

Measured on a container holding 26,832 directories, with docker's own reads served from fixtures so the numbers are the tool's own work:

| run | directories walked | wall clock |
|-----|--------------------|------------|
| the index answers every project | 0 | 0.03s |
| `--rescan`, page cache warm | 26,832 | 0.36s |
| first walk, page cache cold | 26,830 | 26.6s |

The first row is the steady state. Reproduce any of them with `time docker-cleaner --dry-run`, and check the `directories walked` count in the header.

## Reading the plan before you trust it

```sh
docker-cleaner --dry-run --show-kept          # why each surviving resource survived
docker-cleaner --dry-run --json | jq '.'      # the literal argv of every command
docker-cleaner --dry-run --keep 'base/*'      # pin images by glob
```

Exit codes: `0` done (or a dry run, or a declined prompt), `1` some removals were rejected, `2` bad usage, `3` docker cannot be read, so no plan was trusted.

Every flag is listed in [docs/cmdline_args.txt](docs/cmdline_args.txt), generated from the tool's own help.

## Building

`go-toolchain` in the repository root builds it, runs the unit tests with coverage, and runs the `dats/` end-to-end suite. The suite needs a sandbox backend (`bubblewrap` or docker).
