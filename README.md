# VEYRA

Encrypted backups, project checkpoints, and recovery tools for developers.

VEYRA is an experimental Go CLI using restic's encrypted repository format.
It needs no cloud service, Git account, or coding agent. Keep another backup
of irreplaceable data. See [security and recovery](SECURITY.md) for limitations.

## Features

- Encrypted, incremental, deduplicated directory backups to local or mounted drives.
- Snapshot history, browsing, comparisons, integrity checks, and storage statistics.
- Verified restoration into a new directory, including selective file recovery.
- Checkpoints containing modified, untracked, and ignored project files.
- Read-only Git status and protected command execution with optional tests.
- Deletion and pruning previews; explicit confirmation is required to apply them.

## Installation

Install Go 1.23 or later and Python 3, then build from this checkout:

```sh
python scripts/install-restic.py --dir ./bin
go build -o ./bin/veyra ./cmd/veyra
```

Add `bin` to your PATH. On Windows, build with `-o ./bin/veyra.exe`.
The installer downloads restic **0.18.1** and checks its published SHA-256.
Restic is a separate BSD-2-Clause licensed runtime dependency; VEYRA does not
implement its encryption, chunking, or repository storage engine.

## Usage

Flags precede positional arguments. Omit `--password-file` to use restic's secure
terminal prompt. For automation, use a private password file outside all sources
(`chmod 600` on Unix; restrict its ACL on Windows). There is no password argument.

```sh
veyra init --repo /media/backup/veyra
veyra backup --repo /media/backup/veyra --exclude node_modules ./documents ./app
veyra snapshots --repo /media/backup/veyra
veyra checkpoint --repo /media/backup/veyra --label before-refactor ./app
veyra checkpoints --repo /media/backup/veyra
veyra files --repo /media/backup/veyra latest
veyra diff --repo /media/backup/veyra SNAPSHOT_A SNAPSHOT_B
veyra restore --repo /media/backup/veyra --target ./recovered --include /src/main.go SNAPSHOT_ID
veyra verify --repo /media/backup/veyra --full
veyra stats --repo /media/backup/veyra
veyra git --project ./app --json
```

Restoring never overwrites an existing directory. A single-source snapshot
restores its source contents directly into the target. For multiple sources,
their absolute hierarchy is preserved; `--root /snapshot/subfolder` selects a
subfolder. `--include` patterns are relative to the selected root. Restore
preflight streams and authenticates the selected snapshot before extraction;
restic then verifies restored file contents. This reads the selected data twice.

Checkpoints exclude `.git` by default (`--full-git` includes it). Ignored files
are **included**: use repeated `--exclude` patterns to omit build outputs or
local secrets deliberately. VEYRA does not read or execute project configuration.

Wrap an explicit command to create and fully verify a checkpoint before it runs:

```sh
veyra run --repo /media/backup/veyra --project ./app --post \
  --test-arg go --test-arg test --test-arg ./... -- gofmt -w .
```

Each `--test-arg` supplies one argument, including the test executable first.
No shell parses these arguments. The command runs in the project directory.
`--timeout 10m` limits the command and, separately, its tests. Nonzero command
status is preserved; if the command succeeds, a failed test supplies the exit
status. Timeout returns 124. Tests and post-checkpoints can run after command
failure; files are never automatically reverted. Full pre-operation verification
reads the entire repository and may take time. Excluded files are not protected.

Use `--json` for structured results and `--quiet` to suppress normal output.
Wrapped command output goes to stderr in JSON mode. Backend JSON formats follow
restic 0.18.1. Use each command's `--help` for options.

```sh
veyra forget --repo /media/backup/veyra SNAPSHOT_ID           # preview
veyra forget --repo /media/backup/veyra --confirm SNAPSHOT_ID # delete snapshot
veyra prune --repo /media/backup/veyra                       # preview
veyra prune --repo /media/backup/veyra --confirm              # reclaim unused data
```

## Development

With the installed restic executable on PATH:

```sh
python scripts/test_install.py
go test -v ./...
go test -race ./...
go vet ./...
go build ./cmd/veyra
```

Integration tests use disposable repositories; they skip if restic is missing.
CI installs the pinned backend and runs native Ubuntu, Windows, and macOS tests.
Native platform support remains provisional until those jobs pass. Benchmarks
are available with `go test ./internal/project -run '^$' -bench .`.

## License

MIT. See [LICENSE](LICENSE).
