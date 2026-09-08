setup:
	- cmd: 'for d in "$DATS_BIN_DIR" ./build .; do if [ -x "$d/docker-cleaner" ]; then printf %s "$d/docker-cleaner" > {shared.bin}; break; fi; done; test -s {shared.bin} || { echo "docker-cleaner not found; looked in DATS_BIN_DIR=$DATS_BIN_DIR, ./build and ." >&2; exit 1; }'
	- cmd: 'mkdir -p {shared.state} && cp dats/fixtures/state/*.json dats/fixtures/state/*.txt {shared.state}/ && sed -e "s/@@OLD@@/$(date -u -Iseconds -d "-400 days")/g" -e "s/@@RECENT@@/$(date -u -Iseconds -d "-45 days")/g" dats/fixtures/state/container_inspect.json.tmpl > {shared.state}/container_inspect.json'

tests:
	- desc: help names the tool and the compose rule
	  cmd: '$(cat {shared.bin}) --help'
	  outputs:
		stdout:
			- "docker-cleaner [flags]"
			- "Deleting its compose file is what"
			- "--dry-run"
			- "--build-cache-age"

	- desc: a dry run issues no command at all
	  cmd: 'mkdir -p {outputs.empty}; B=$(cat {shared.bin}); FAKE_DOCKER_STATE={shared.state} FAKE_DOCKER_LOG={outputs.exec.log} $B --dry-run --docker-bin dats/fixtures/fake-docker --index {outputs.index.json} --mountinfo {inputs.mountinfo} --docker-root {outputs.dockerroot}'
	  inputs:
		files:
			mountinfo: "27 1 259:2 / {outputs.empty} rw,relatime shared:1 - ext4 /dev/sda1 rw\n"
	  outputs:
		stdout:
			- "DRY RUN"
			- "web-old"
			- "myapp:v1"
			- "orphan_default"
			- "Dry run: nothing was removed."
		!stdout:
			- "APPLY"
		!files:
			exec.log:
				exists: true

	- desc: a run says what it is doing while it does it, on stderr, leaving stdout to the report
	  cmd: 'mkdir -p {outputs.empty}; B=$(cat {shared.bin}); FAKE_DOCKER_STATE={shared.state} $B --dry-run --docker-bin dats/fixtures/fake-docker --index {outputs.index.json} --mountinfo {inputs.mountinfo} --docker-root {outputs.dockerroot}'
	  inputs:
		files:
			mountinfo: "27 1 259:2 / {outputs.empty} rw,relatime shared:1 - ext4 /dev/sda1 rw\n"
	  outputs:
		stderr:
			- "docker-cleaner: asking docker what it holds"
			- "0% (0 of 4)"
			- "measuring volume "
			- "docker-cleaner: looking for compose projects"
			- "docker-cleaner: deciding what to remove"
		!stderr:
			- "["
		!stdout:
			- "docker-cleaner: reading containers"

	- desc: a docker call that takes its time keeps saying so, instead of looking wedged
	  cmd: 'mkdir -p {outputs.empty}; B=$(cat {shared.bin}); FAKE_DOCKER_STATE={shared.state} FAKE_DOCKER_STALL=7 $B --dry-run --docker-bin dats/fixtures/fake-docker --index {outputs.index.json} --mountinfo {inputs.mountinfo} --docker-root {outputs.dockerroot}'
	  inputs:
		files:
			mountinfo: "27 1 259:2 / {outputs.empty} rw,relatime shared:1 - ext4 /dev/sda1 rw\n"
	  outputs:
		stdout:
			- "DRY RUN"
		stderr:
			- "reading volumes 1 to 3 of 3 ["

	- desc: progress never says nothing at all
	  cmd: 'mkdir -p {outputs.empty}; B=$(cat {shared.bin}); FAKE_DOCKER_STATE={shared.state} $B --dry-run --progress never --docker-bin dats/fixtures/fake-docker --index {outputs.index.json} --mountinfo {inputs.mountinfo} --docker-root {outputs.dockerroot}'
	  inputs:
		files:
			mountinfo: "27 1 259:2 / {outputs.empty} rw,relatime shared:1 - ext4 /dev/sda1 rw\n"
	  outputs:
		stdout:
			- "DRY RUN"
		!stderr:
			- "docker-cleaner: reading"

	- desc: a --progress it does not know is a usage error, not a silent default
	  cmd: 'B=$(cat {shared.bin}); FAKE_DOCKER_STATE={shared.state} $B --dry-run --progress loud --docker-bin dats/fixtures/fake-docker'
	  exit: 2
	  outputs:
		stderr:
			- "--progress"
			- "auto, always, never"

	- desc: a search that runs out of time keeps every project it could not resolve
	  cmd: 'mkdir -p {outputs.proj}; B=$(cat {shared.bin}); FAKE_DOCKER_STATE=$(dirname {inputs.version.json}) $B --dry-run --scan-timeout 1ns --docker-bin dats/fixtures/fake-docker --index {outputs.index.json} --mountinfo {inputs.mountinfo} --docker-root {outputs.dockerroot} --show-kept'
	  inputs:
		files:
			version.json: '{"Client":{"Version":"29.3.1"},"Server":{"Version":"29.3.1"}}'
			ps.txt: ""
			image_ls.json: ""
			network_ls.json: ""
			buildx_ls.json: ""
			volume_ls.json: '{"Name":"webapp_pgdata"}'
			volume_inspect.json: '[{"Name":"webapp_pgdata","Driver":"local","Scope":"local","Labels":{"com.docker.compose.project":"webapp"}}]'
			mountinfo: "27 1 259:2 / {outputs.proj} rw,relatime shared:1 - ext4 /dev/sda1 rw\n"
	  outputs:
		stdout:
			- "SEARCH INCOMPLETE"
			- "COULD NOT SEARCH"
			- "the search was incomplete"
		!stdout:
			- "VOLUMES TO REMOVE"

	- desc: an apply removes exactly what the plan listed
	  cmd: 'mkdir -p {outputs.empty}; B=$(cat {shared.bin}); FAKE_DOCKER_STATE={shared.state} FAKE_DOCKER_LOG={outputs.exec.log} $B --yes --docker-bin dats/fixtures/fake-docker --index {outputs.index.json} --mountinfo {inputs.mountinfo} --docker-root {outputs.dockerroot}'
	  inputs:
		files:
			mountinfo: "27 1 259:2 / {outputs.empty} rw,relatime shared:1 - ext4 /dev/sda1 rw\n"
	  outputs:
		files:
			exec.log:
				match:
					- "(?m)^FAKE-DOCKER-EXEC: rm c1$"
					- "(?m)^FAKE-DOCKER-EXEC: rm c3$"
					- "(?m)^FAKE-DOCKER-EXEC: rmi myapp:v1$"
					- "(?m)^FAKE-DOCKER-EXEC: rmi sha256:9999999999999999999999999999999999999999999999999999999999999999$"
					- "(?m)^FAKE-DOCKER-EXEC: volume rm orphan_data$"
					- "(?m)^FAKE-DOCKER-EXEC: volume rm scratch$"
					- "(?m)^FAKE-DOCKER-EXEC: network rm n2$"
					- "(?m)^FAKE-DOCKER-EXEC: buildx prune --builder default --force --filter until=168h$"
					- "(?m)^FAKE-DOCKER-EXEC: buildx prune --builder ci-builder --force --filter until=168h$"
				notMatch:
					- "rmi myapp:v2"
					- "rmi nginx:latest"
					- "rmi nginx:1.27"
					- "rmi postgres:16"
					- "volume rm pgdata-live"
					- "rm c2"
					- "network rm n1"
		stdout:
			- "APPLY"
			- "FREED"
		!stderr:
			- "system df"

	- desc: age reaches the selector, so a wider window keeps the recent container
	  cmd: 'mkdir -p {outputs.empty}; B=$(cat {shared.bin}); FAKE_DOCKER_STATE={shared.state} $B --dry-run --age 90d --docker-bin dats/fixtures/fake-docker --index {outputs.index.json} --mountinfo {inputs.mountinfo} --docker-root {outputs.dockerroot}'
	  inputs:
		files:
			mountinfo: "27 1 259:2 / {outputs.empty} rw,relatime shared:1 - ext4 /dev/sda1 rw\n"
	  outputs:
		stdout:
			- "CONTAINERS TO REMOVE  (1,"
			- "web-old"
		!stdout:
			- "probe-recent "

	- desc: the cache threshold is its own, and only it moves the prune filter
	  cmd: 'mkdir -p {outputs.empty}; B=$(cat {shared.bin}); FAKE_DOCKER_STATE={shared.state} $B --dry-run --build-cache-age 90d --docker-bin dats/fixtures/fake-docker --index {outputs.index.json} --mountinfo {inputs.mountinfo} --docker-root {outputs.dockerroot}'
	  inputs:
		files:
			mountinfo: "27 1 259:2 / {outputs.empty} rw,relatime shared:1 - ext4 /dev/sda1 rw\n"
	  outputs:
		stdout:
			- "buildx prune --builder default --force --filter until=2160h"
			- "CONTAINERS TO REMOVE  (2,"
		!stdout:
			- "until=168h"

	- desc: each step flag empties its own section
	  cmd: 'mkdir -p {outputs.empty}; B=$(cat {shared.bin}); FAKE_DOCKER_STATE={shared.state} $B --dry-run --no-images --no-volumes --no-networks --no-build-cache --docker-bin dats/fixtures/fake-docker --index {outputs.index.json} --mountinfo {inputs.mountinfo} --docker-root {outputs.dockerroot}'
	  inputs:
		files:
			mountinfo: "27 1 259:2 / {outputs.empty} rw,relatime shared:1 - ext4 /dev/sda1 rw\n"
	  outputs:
		stdout:
			- "CONTAINERS TO REMOVE"
		!stdout:
			- "IMAGES TO REMOVE"
			- "VOLUMES TO REMOVE"
			- "NETWORKS TO REMOVE"
			- "BUILD CACHE"

	- desc: keep pins an image the age rule would have taken
	  cmd: 'mkdir -p {outputs.empty}; B=$(cat {shared.bin}); FAKE_DOCKER_STATE={shared.state} $B --dry-run --keep "myapp:*" --docker-bin dats/fixtures/fake-docker --index {outputs.index.json} --mountinfo {inputs.mountinfo} --docker-root {outputs.dockerroot}'
	  inputs:
		files:
			mountinfo: "27 1 259:2 / {outputs.empty} rw,relatime shared:1 - ext4 /dev/sda1 rw\n"
	  outputs:
		stdout:
			- "matches --keep"
		!stdout:
			- "myapp:v1  "

	- desc: json carries the literal argv of every planned command
	  cmd: 'mkdir -p {outputs.empty}; B=$(cat {shared.bin}); FAKE_DOCKER_STATE={shared.state} FAKE_DOCKER_LOG={outputs.exec.log} $B --json --dry-run --docker-bin dats/fixtures/fake-docker --index {outputs.index.json} --mountinfo {inputs.mountinfo} --docker-root {outputs.dockerroot}'
	  inputs:
		files:
			mountinfo: "27 1 259:2 / {outputs.empty} rw,relatime shared:1 - ext4 /dev/sda1 rw\n"
	  outputs:
		stdout:
			- '"dry_run": true'
			- '"compose_search_complete": true'
			- '"rm",'
			- '"c1"'
			- '"until=168h"'
		!files:
			exec.log:
				exists: true

	- desc: json without dry-run or yes is refused rather than corrupted by a prompt
	  cmd: 'B=$(cat {shared.bin}); FAKE_DOCKER_STATE={shared.state} $B --json --docker-bin dats/fixtures/fake-docker'
	  exit: 2
	  outputs:
		stderr:
			- "--json needs --dry-run or --yes"

	- desc: a bad age is a usage error
	  cmd: 'B=$(cat {shared.bin}); FAKE_DOCKER_STATE={shared.state} $B --dry-run --age tuesday --docker-bin dats/fixtures/fake-docker'
	  exit: 2
	  outputs:
		stderr:
			- "--age"
			- "is not a duration"

	- desc: a docker binary that does not resolve stops the run
	  cmd: 'B=$(cat {shared.bin}); $B --dry-run --docker-bin ./no-such-docker'
	  exit: 3
	  outputs:
		stderr:
			- "cannot run"

	- desc: an unreachable daemon stops the run before anything is planned
	  cmd: 'mkdir -p {outputs.state}; cp {shared.state}/* {outputs.state}/; printf %s "{\"Client\":{\"Version\":\"29.3.1\"}}" > {outputs.state}/version.json; B=$(cat {shared.bin}); FAKE_DOCKER_STATE={outputs.state} FAKE_DOCKER_LOG={outputs.exec.log} $B --yes --docker-bin dats/fixtures/fake-docker'
	  exit: 3
	  outputs:
		stderr:
			- "daemon is not reachable"
		!files:
			exec.log:
				exists: true

	- desc: nothing can answer a prompt with no terminal, so the run refuses
	  cmd: 'mkdir -p {outputs.empty}; B=$(cat {shared.bin}); FAKE_DOCKER_STATE={shared.state} FAKE_DOCKER_LOG={outputs.exec.log} $B --docker-bin dats/fixtures/fake-docker --index {outputs.index.json} --mountinfo {inputs.mountinfo} --docker-root {outputs.dockerroot}'
	  exit: 2
	  inputs:
		files:
			mountinfo: "27 1 259:2 / {outputs.empty} rw,relatime shared:1 - ext4 /dev/sda1 rw\n"
	  outputs:
		stderr:
			- "refusing to prompt with no terminal"
		!files:
			exec.log:
				exists: true

	- desc: a container that would not go keeps the image it holds
	  cmd: 'mkdir -p {outputs.empty}; B=$(cat {shared.bin}); FAKE_DOCKER_STATE={shared.state} FAKE_DOCKER_LOG={outputs.exec.log} FAKE_DOCKER_FAIL="rm c1" $B --yes --docker-bin dats/fixtures/fake-docker --index {outputs.index.json} --mountinfo {inputs.mountinfo} --docker-root {outputs.dockerroot}'
	  exit: 1
	  inputs:
		files:
			mountinfo: "27 1 259:2 / {outputs.empty} rw,relatime shared:1 - ext4 /dev/sda1 rw\n"
	  outputs:
		stdout:
			- "FAILED   docker rm c1"
			- "skipped  myapp:v1: web-old was not removed"
		files:
			exec.log:
				match:
					- "(?m)^FAKE-DOCKER-EXEC: rm c1$"
				notMatch:
					- "rmi myapp:v1"

	- desc: a stack that is down keeps its volume while its compose file exists
	  cmd: 'mkdir -p {outputs.proj}; cp {inputs.docker-compose.yml} {outputs.proj}/docker-compose.yml; B=$(cat {shared.bin}); FAKE_DOCKER_STATE=$(dirname {inputs.version.json}) $B --dry-run --docker-bin dats/fixtures/fake-docker --index {outputs.index.json} --mountinfo {inputs.mountinfo} --docker-root {outputs.dockerroot} --show-kept'
	  inputs:
		files:
			version.json: '{"Client":{"Version":"29.3.1"},"Server":{"Version":"29.3.1"}}'
			ps.txt: ""
			image_ls.json: ""
			network_ls.json: ""
			buildx_ls.json: ""
			volume_ls.json: '{"Name":"webapp_pgdata"}'
			volume_inspect.json: '[{"Name":"webapp_pgdata","Driver":"local","Scope":"local","Labels":{"com.docker.compose.project":"webapp"}}]'
			compose_config.json: '{"name":"webapp","services":{"db":{"image":"postgres:16"}},"volumes":{"pgdata":{"name":"webapp_pgdata"}}}'
			docker-compose.yml: "services:\n  db:\n    image: postgres:16\n"
			mountinfo: "27 1 259:2 / {outputs.proj} rw,relatime shared:1 - ext4 /dev/sda1 rw\n"
	  outputs:
		stdout:
			- "search complete"
			- "webapp_pgdata"
			- "claimed by a compose project still on disk"
		!stdout:
			- "VOLUMES TO REMOVE"

	- desc: deleting the compose file is what retires the project
	  cmd: 'mkdir -p {outputs.proj}; B=$(cat {shared.bin}); FAKE_DOCKER_STATE=$(dirname {inputs.version.json}) $B --dry-run --docker-bin dats/fixtures/fake-docker --index {outputs.index.json} --mountinfo {inputs.mountinfo} --docker-root {outputs.dockerroot}'
	  inputs:
		files:
			version.json: '{"Client":{"Version":"29.3.1"},"Server":{"Version":"29.3.1"}}'
			ps.txt: ""
			image_ls.json: ""
			network_ls.json: ""
			buildx_ls.json: ""
			volume_ls.json: '{"Name":"webapp_pgdata"}'
			volume_inspect.json: '[{"Name":"webapp_pgdata","Driver":"local","Scope":"local","Labels":{"com.docker.compose.project":"webapp"}}]'
			mountinfo: "27 1 259:2 / {outputs.proj} rw,relatime shared:1 - ext4 /dev/sda1 rw\n"
	  outputs:
		stdout:
			- "search complete"
			- "VOLUMES TO REMOVE"
			- "webapp_pgdata"

	- desc: a search that could not run everywhere keeps the volume instead
	  cmd: 'B=$(cat {shared.bin}); FAKE_DOCKER_STATE=$(dirname {inputs.version.json}) $B --dry-run --docker-bin dats/fixtures/fake-docker --index {outputs.index.json} --mountinfo {inputs.mountinfo} --docker-root {outputs.dockerroot} --show-kept'
	  inputs:
		files:
			version.json: '{"Client":{"Version":"29.3.1"},"Server":{"Version":"29.3.1"}}'
			ps.txt: ""
			image_ls.json: ""
			network_ls.json: ""
			buildx_ls.json: ""
			volume_ls.json: '{"Name":"webapp_pgdata"}'
			volume_inspect.json: '[{"Name":"webapp_pgdata","Driver":"local","Scope":"local","Labels":{"com.docker.compose.project":"webapp"}}]'
			mountinfo: "27 1 259:2 / {outputs.never-mounted} rw,relatime shared:1 - ext4 /dev/sda1 rw\n"
	  outputs:
		stdout:
			- "SEARCH INCOMPLETE"
			- "COULD NOT SEARCH"
			- "the search was incomplete"
		!stdout:
			- "VOLUMES TO REMOVE"

	- desc: a project the index already knows costs no walk
	  cmd: 'mkdir -p {outputs.proj}; cp {inputs.docker-compose.yml} {outputs.proj}/docker-compose.yml; B=$(cat {shared.bin}); S=$(dirname {inputs.version.json}); FAKE_DOCKER_STATE=$S $B --dry-run --docker-bin dats/fixtures/fake-docker --index {outputs.index.json} --mountinfo {inputs.mountinfo} --docker-root {outputs.dockerroot} > {outputs.first.txt}; FAKE_DOCKER_STATE=$S $B --dry-run --docker-bin dats/fixtures/fake-docker --index {outputs.index.json} --mountinfo {inputs.mountinfo} --docker-root {outputs.dockerroot}'
	  inputs:
		files:
			version.json: '{"Client":{"Version":"29.3.1"},"Server":{"Version":"29.3.1"}}'
			ps.txt: ""
			image_ls.json: ""
			network_ls.json: ""
			buildx_ls.json: ""
			volume_ls.json: '{"Name":"webapp_pgdata"}'
			volume_inspect.json: '[{"Name":"webapp_pgdata","Driver":"local","Scope":"local","Labels":{"com.docker.compose.project":"webapp"}}]'
			compose_config.json: '{"name":"webapp","volumes":{"pgdata":{"name":"webapp_pgdata"}}}'
			docker-compose.yml: "services:\n  db:\n    image: postgres:16\n"
			mountinfo: "27 1 259:2 / {outputs.proj} rw,relatime shared:1 - ext4 /dev/sda1 rw\n"
	  outputs:
		stdout:
			- "0 directories walked"
		!stdout:
			- "VOLUMES TO REMOVE"
		files:
			index.json:
				match:
					- "webapp"
