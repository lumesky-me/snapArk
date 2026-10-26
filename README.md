# VEYRA

Encrypted backups, project checkpoints, and recovery tools for developers.

VEYRA is an experimental Go CLI using restic's encrypted repository format.
It needs no cloud service or account. Keep another backup
of irreplaceable data. See [security and recovery](SECURITY.md) for limitations.

## Features

- Encrypted, incremental, deduplicated directory backups to local or mounted drives.
- Snapshot history, browsing, comparisons, integrity checks, and storage statistics.
- Guided setup and recovery, with verified restoration into a new directory.
- Named profiles, current-user scheduling, and verification history.
- Checkpoints containing modified, untracked, and ignored project files.
- Read-only Git status and protected command execution with optional tests.
- Deletion and pruning previews; explicit confirmation is required to apply them.

## Installation

Downloadable test builds are available under the **Artifacts** section of successful
[GitHub Actions runs](https://github.com/lumesky-me/veyra/actions). These are
experimental builds, not official releases. GitHub requires signing in to download
artifacts. Extract the artifact wrapper, verify the platform archive against its
`SHA256SUMS` (Linux: `sha256sum -c SHA256SUMS`; macOS:
`shasum -a 256 -c SHA256SUMS`; Windows:
`Get-FileHash .\veyra-windows-amd64.zip -Algorithm SHA256` and compare the hash),
then extract the platform archive. Run `./veyra setup` (`.\veyra.exe setup` in
PowerShell). Setup can install the pinned backend with your approval, create a
profile, and make a verified first backup. Choose the extracted directory for the
backend, or let the profile remember another location. No Go compiler or Python
is needed. Add VEYRA's directory to PATH yourself if desired. Checksums detect damage; obtain them from the same
trusted Actions run as the archive.

Archives are available for Linux x86-64/ARM64, macOS Intel/Apple silicon, and
Windows x86-64. Filesystem permissions, links, and user-session scheduling differ
between platforms; see the [platform limitations](SECURITY.md#restore-boundaries).
`veyra version --json` reports the source revision, target, and Go toolchain.

To build from source, install Go 1.23 or later, clone this repository,
and run these commands from the checkout:

```sh
go build -o ./bin/veyra ./cmd/veyra
./bin/veyra setup
```

Add `bin` to your PATH. On Windows, build with `-o ./bin/veyra.exe`.
The built-in installer downloads restic **0.18.1**, checks an embedded SHA-256,
and refuses existing executables. It never changes PATH. For scripted installation:
`veyra setup --install-restic --dir ./bin --confirm`. The Python installer remains
available as `python scripts/install-restic.py --dir ./bin`.
Restic is a separate BSD-2-Clause licensed runtime dependency; VEYRA does not
implement its encryption, chunking, or repository storage engine.

## Usage

Flags precede positional arguments. Omit `--password-file` to enter a hidden
password once per command in a terminal. For automation, use a private password file outside all sources
(`chmod 600` on Unix; restrict its ACL on Windows). There is no password argument.

```sh
veyra init --repo /media/backup/veyra
veyra backup --repo /media/backup/veyra --exclude node_modules ./documents ./app
veyra recover --repo /media/backup/veyra
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

## Profiles and daily backups

```sh
veyra profile add --repo /media/backup/app --source ./app --exclude node_modules --keep-last 10 app
veyra plan --profile app --password-file /private/backup-password
veyra backup --profile app --password-file /private/backup-password
veyra status --password-file /private/backup-password
veyra doctor --profile app --password-file /private/backup-password
veyra find --profile app --query main.go --password-file /private/backup-password
veyra retention --profile app --password-file /private/backup-password
veyra schedule --profile app --password-file /private/backup-password
```

Profiles store non-secret settings in your user configuration directory. Retention
and scheduling are previews by default; neither runs automatically during backup.
See [profiles and daily use](docs/profiles.md) for scope, confirmation, scheduling,
configuration recovery, and diagnostic limitations. Protected commands can use a single-source profile with `veyra run --profile app`.
Only included, non-excluded files are protected.

## Verification and rehearsal

```sh
veyra verify --profile app --full --password-file /private/backup-password
veyra history --profile app --json
veyra test-restore --profile app --full --password-file /private/backup-password
```

Checks, backup attempts, and full recovery rehearsals have separate local history.
Status matches records to the current repository identity and snapshot scope;
old results are not a current health guarantee. Current-user scheduling is opt-in:
inspect `schedule preview`, then use `schedule install --confirm`. Linux uses
systemd, Windows uses interactive Task Scheduler tasks, and macOS uses LaunchAgents.
User-session availability matters; logged-out Windows execution is unsupported.
See the [workflow guide](docs/profiles.md) for unattended execution, journal limits,
private recovery directories, and current platform limitations.

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
Check the Actions result for the exact commit and platform you use. Benchmarks
are available with `go test ./internal/project -run '^$' -bench .`.

Build all five distribution archives with `python scripts/build.py`; validate checksums
and run the native archived executable with `python scripts/check-build.py`. Verification requires restic on PATH and exercises packaged profile backup/recovery.
Each includes
license notices and backend installation assistance; restic itself is installed
separately. This command prepares files in `dist/` and does not publish a release.

## License

MIT. See [LICENSE](LICENSE).
