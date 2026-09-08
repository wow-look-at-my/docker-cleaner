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

Docker already knows where those files are. Every container carries its project's compose files and working directory in a label, running or stopped. The tool copies that into an index at `/var/lib/docker-cleaner/projects.json`, so the paths survive the `down` that deletes the containers. A run is label reads, index lookups and `stat` calls. It never crawls the disk.

A project neither explains is looked for beside the project directories docker did name. Compose names a project after its own directory, and stacks sit together, so this costs a directory read per parent.

A project nothing has ever named a file for is kept, not removed. Retirement needs evidence: a path docker gave us that is now gone. Not looking is never evidence of deletion.

## While it works

A run reports each step on stderr. It names the docker read it waits on and the volume it is measuring, over a fraction of the calls it has left. On a terminal it redraws a line in place. Anywhere else it prints a line per change, so a log keeps every step. `--progress never` turns it off. Only the report goes to stdout, so `--json` stays parseable either way.

`Ctrl-C` stops a run at once.

### Optional: `docker-cleaner watch`

Docker forgets a project's file paths when its containers go, so a stack brought up and down between two cleanups leaves nothing to read. `docker-cleaner watch` tails `docker events` and records those paths as containers are created. It is an optimisation, never a requirement. A project it misses is looked for beside the ones docker still names, and a project found nowhere is kept.

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

Compose discovery reads no directory the projects do not live in, so its cost does not grow with the disk. What is left is docker's own reads, which run at the same time as each other, and the volume measurement.

A volume is measured by walking what its driver mounts, because the daemon offers no other way to size one. Those reads run several at a time inside the volume in hand. A volume holding a nested docker root therefore does not hold the run up on a single reader.

No decision about what to remove reads a size. A volume the tool cannot read is reported as unmeasured. The plan is unaffected.

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
