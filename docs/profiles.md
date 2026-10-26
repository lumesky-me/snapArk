# snapArk — Profiles & Daily Use

**Everything you need to configure, schedule, verify, and recover encrypted backups.**

A *profile* remembers your source folders, encrypted backup destination, exclusions, and verification preferences.

> **During the rename:** Existing builds still use `veyra` as the executable. Replace `snapark` with `veyra` until Codex finishes the rename. Existing configuration paths and restore marker names are shown as they actually exist.

## Contents

- [Quick start](#quick-start)
- [Profile settings](#profile-settings)
- [Selecting files](#selecting-files)
- [Backup status and scope](#backup-status-and-scope)
- [Recovery and retention](#recovery-and-retention)
- [Scheduling](#scheduling)
- [Verification and recovery rehearsal](#verification-history-and-recovery-rehearsal)
- [Guided setup and recovery](#guided-setup-and-recovery)
- [Maintaining local history](#maintaining-local-history)

## Quick start

Run `snapark setup` for a guided first backup, or use an existing profile:

```sh
snapark backup --profile app
snapark status
snapark recover --profile app
```

Commands without `--password-file` securely prompt for the repository password. To schedule unattended backups, use an absolute private password-file path outside every backed-up source.

## Profile settings

Profiles are version-1 JSON in the operating system's per-user configuration
directory under `veyra/profiles.json`. `--config /absolute/path/profiles.json`
selects another file. No passwords or commands are stored there. Password-file
paths remain explicit runtime arguments. Configuration inside a source or backup
repository is rejected; for a whole-home backup choose an external config location.

<details>
<summary><strong>More profile commands</strong></summary>

```sh
snapark profile add --repo /media/backup/app --source /home/me/app \
  --exclude node_modules --keep-last 10 --keep-daily 7 --keep-monthly 6 app
snapark profile default app
snapark profile list
snapark profile show app
snapark profile validate app
snapark plan --profile app --password-file /private/backup-password
snapark backup --profile app --password-file /private/backup-password
snapark status --password-file /private/backup-password
snapark doctor --profile app --password-file /private/backup-password
```

</details>

### Names and changes

Options precede the profile name. Names use letters, digits, underscores, and
hyphens. Paths are resolved at creation, so profiles work from other directories.
`profile replace` takes a complete replacement configuration and preserves the
profile ID. `profile delete` removes only configuration. Neither deletes backups.
Use `--disabled` when adding/replacing to prevent profile backups and scheduling.
`--expected-hours 24` controls status warnings; zero disables freshness warnings.
`--verify metadata` (default) or `--verify full` checks the repository after backup.
A verification failure returns failure even if a snapshot was already stored.
Profile backup sources and exclusions cannot be overridden on the command line;
replace the profile configuration so status and retention keep their scope accurate.

## Selecting files

**Anything excluded cannot be recovered from that snapshot.**

Repeated `--include` settings select literal relative files/directories within
sources, not glob patterns. No include list means all source contents. Exclusion
patterns then apply using restic's rules. Missing include paths are ignored only
when other include paths exist; an entirely empty selection fails. Symlink
selections resolving outside a source are rejected. Explicit directory sources are
resolved to their physical roots, so a source-directory symlink or junction backs
up its contents rather than only the link. Internal links retain restic behavior.
Planning shows both configured and resolved source roots. Password files cannot be
inside any source. Profile checkpoints additionally exclude `.git` by default.

## Backup status and scope

A snapshot only counts as a completed backup for the right profile, host, and source revision.

Profile scope includes an opaque profile ID, source/filter revision, and current
hostname. Changing source/filter settings starts a new scope. Status and retention
will not treat another host, another profile, an old filter revision, or a profile
checkpoint as proof of a current ordinary backup. Hostname changes require manual
inspection of historical snapshots. Status also checks recorded physical source
roots, so redirecting a directory alias does not make a snapshot of its former
target count as a backup of the new target. `status` reads authoritative repository
metadata; it does not store or invent a last-success flag. Verification history is stored locally and associated by authenticated repository
identity and observed snapshot IDs; a replaced repository does not inherit it. Profile backups use a pending tag until backup and configured
verification succeed, then publish a completion tag using restic's own tag operation.
Status warns about newer pending work, and retention never selects pending snapshots.
An interrupted completion step can leave a fully written snapshot pending; inspect
it with explicit `--repo`. Earlier unmarked profile snapshots remain recoverable
with `--repo` but are not retroactively certified as successful. JSON backup output
is a stream of restic events followed by a `completed` event carrying the final
snapshot ID; tagging changes that ID, so use the final completion ID for recovery. Unavailable repositories are not reported as empty.

### Safe backup previews

`plan` opens repository metadata, authenticates with the supplied password source,
and runs restic's dry-run scanner without repository locks or writes. Counts are
estimates at scan time; deduplicated growth and full integrity are not established.
`doctor` uses shallow checks by default. `doctor --full` reads and authenticates
all packs explicitly. Available filesystem bytes are observed where supported, not predicted backup
capacity. Writer-lock health is unknown; diagnostics never remove locks.
Nonzero status/doctor results mean a problem or overdue/missing backup was observed.

### Developer checkpoints

Single-source profiles also work with `run --profile app -- COMMAND ARG...`.
The project defaults to that source; an explicit `--project` must match it.
Configured includes/exclusions apply to pre/post checkpoints: excluded data is not
protected. Multi-source profiles require an explicit repository/project workflow
instead. Protected-command checkpoints use the checkpoint scope and do not count
as ordinary profile backups for status or retention.

## Recovery and retention

**Restores never overwrite an existing directory.** Review your snapshot and choose a new destination.


```sh
snapark snapshots --profile app --password-file /private/backup-password
snapark find --profile app --query main.go --password-file /private/backup-password
snapark restore --profile app --target ./recovered --dry-run --password-file /private/backup-password latest
snapark restore --profile app --target ./recovered --include /src/main.go --password-file /private/backup-password latest
snapark retention --profile app --password-file /private/backup-password
snapark retention --profile app --confirm --password-file /private/backup-password
```

### Finding snapshots

`find` searches literal substrings of original snapshot paths. It does not search
file contents. Exact snapshot IDs avoid ambiguity when recovering historical
profile revisions: use explicit `--repo` to inspect snapshots outside current scope.
Include-based snapshots may retain their absolute path hierarchy; inspect `files`
and use `restore --root /original/project` to choose the desired root. Single-file
snapshots restore into a new directory containing that file.

### Incomplete restores

Restore previews create neither a destination nor a status marker. A real restore
reserves `<target>.veyra-incomplete.json` beside its destination before extraction.
Failure or interruption retains that marker and the partial target. Only a verified
successful restore removes it. A pre-existing marker blocks a new attempt until
it is inspected. Never treat a marked destination as a completed recovery.

### Retention is opt-in

Retention and pruning are never silently applied during normal backup.

Retention previews restic's keep/forget decisions, grouped by host and source
paths and limited to the selected profile scope. `--confirm` applies deletion.
Keep-last/daily/weekly/monthly/yearly policies combine using restic's keep rules.
Maintenance never runs during ordinary backup. It does not automatically prune;
`prune --repo ...` previews repository-wide unreferenced-data removal and requires
its own `--confirm`. Deleted historical recovery points may be unrecoverable after
pruning. Neither preview promises a future amount of reclaimed space. After an
interruption inspect snapshot history before retrying. Locks are never auto-removed.

## Scheduling

Scheduling is **optional**. It needs a private password file for unattended runs.

| Platform | Scheduler | Requires |
|---|---|---|
| Windows | Task Scheduler | Logged-in user |
| macOS | LaunchAgents | Desktop login session |
| Linux | systemd user timers | Active user manager |


```sh
snapark credentials --password-file /absolute/private/password
snapark schedule preview --profile app --password-file /absolute/private/password
snapark schedule install --profile app --password-file /absolute/private/password --confirm
snapark schedule status --profile app --json
snapark schedule disable --profile app --confirm
snapark schedule enable --profile app --confirm
snapark schedule remove --profile app --confirm
```

### Safe scheduling changes

Preview installs nothing. Other changes need `--confirm`; ordinary backup/setup
commands never install schedules. The interval is `expected-hours` (24 if zero),
limited to 31 days. Password-file paths are included, never password contents.
A disconnected drive or active backup makes a run fail safely. No retention runs.

### Platform-specific limitations

- Linux uses user-systemd timers. A user manager must be available; snapArk never
  enables lingering. Timers request a run five minutes after manager startup and
  repeat while active; missed intervals are not a persistent calendar schedule.
- Windows uses Task Scheduler's COM API and the current account with
  `InteractiveToken` and least privileges. This requires a logged-in user; it
  does not support logged-out execution. `StartWhenAvailable` requests catch-up,
  subject to Windows session/power policy. `schedule replace --confirm` updates
  an owned, unchanged task. No elevation or account password is requested.
- macOS uses `~/Library/LaunchAgents` in the current GUI login domain. Loading
  requires a desktop login session. `RunAtLoad` requests a run when loaded;
  intervals missed during sleep or logout are not all replayed. Bootstrap
  failures retain the owned plist for inspection; `enable` can retry loading.

### Ownership and scheduler status

**An installed schedule does not prove that a backup ran.**

Status queries the native scheduler and distinguishes installation, enabled state,
loaded state, conflicts and inaccessible services. A loaded timer/job does not
prove that a backup ran. Ownership ties a definition to the profile and config
path; fingerprints detect edits. snapArk refuses to change unrelated or edited
entries. Windows stores its native XML fingerprint beside the configuration in
`<config>.schedules/`; keep those ownership records with the configuration.
Existing macOS/systemd definitions are never overwritten: review, remove and
reinstall to update them. Older systemd definitions without a fingerprint require
manual review and removal before installation through the current CLI.

### Native tests

Native tests use disposable future-dated Windows tasks and temporary macOS jobs,
with cleanup. Plists also pass native `plutil` validation. Real lifecycle tests
require scheduler access and never install permanent jobs on CI machines.


### Configuration locks

Configuration writes use a private temporary file, sync, and atomic rename where
supported. Concurrent cooperative writers get a busy error instead of overwriting
each other. After a crash an orphan `.profiles-*` temp file is ignored. A stale
`profiles.json.lock` directory is never removed automatically: confirm no writer
is active before removing it manually. Malformed/unsupported versions remain
unchanged and must be restored or corrected before editing through the CLI.

## Verification history and recovery rehearsal

**Backup success, integrity checks, and successful recovery tests are different things.** Use full verification or actual recovery rehearsals when appropriate.


```sh
snapark verify --profile app --full --password-file /private/backup-password
snapark history --profile app --json
snapark test-restore --profile app --full --password-file /private/backup-password
snapark backup --profile app --scheduled --password-file /private/backup-password
```

### What each check establishes

History lives beside the selected profile configuration in `<config>.history/`.
Records separate backup completion, metadata checks, full-data authentication,
scheduled attempts, and actual full recovery rehearsals. A full verification reads
all stored data. A rehearsal actually restores and verifies contents in a newly
created private directory, removes only its own directory on success, and preserves
failure evidence. `--temp-dir` selects an existing parent. Default sample mode deterministically selects up to five portable regular files
within a 64 MiB content budget (`--sample-files`, `--max-bytes`). It streams actual
authenticated file content into generated private filenames and independently
rereads/hash-checks it. Sample selection scans snapshot metadata, not all file data.
Samples do not validate links or metadata and do not prove complete recoverability.
`--full` can require space for the entire selected scope.
Existing `--repo`, `snapshots`, `files`, `find`, and exact-ID `restore` commands work
on a replacement computer without the old profile or hostname. Normal profile
status/retention scoping is not relaxed for disaster recovery.

### Interpreting history

A historical check is not a guarantee of current recovery health.

Journal files contain no password values or raw backend errors. Each started event
is atomically replaced with its final result. A remaining `started` event means
completion was not observed; it does not establish whether a process is still
running. The journal is local evidence, not tamper-proof certification. Corrupt or
unsupported records cause errors rather than being overwritten. After 1,000 records,
new journaled work refuses to start; use `history archive` to preserve reviewed records and free space.
A stale journal `.lock` or `run-<profile-id>.lock` requires manual inspection; locks
are never stolen. Status authenticates current repository identity and scope before
matching history. Retagging/deleting/adding snapshots can make prior checks historical;
a scope match still does not guarantee present integrity. Backup-completion events
are not full-verification events. Failed authentication without a known repository
identity remains visible in history but is not attributed to the current repository.

### Unattended credentials

`--scheduled` requires an absolute private password-file path, fails promptly if
another run owns the profile lock, and journals the attempt. Failure categories
separate credential rejection, unavailable/locked repositories, authentication,
source read errors and competing runs without storing raw errors. Task Scheduler
status also reports its native last result and run times; these are distinct from
repository verification. Scheduled execution never runs retention.
The pinned restic binary is found beside snapArk before falling back to PATH. SIGTERM
and Ctrl-C request cancellation; ungraceful process termination can leave a started
record and lock for manual review. Windows unattended credentials must be local,
owned by the current account, and readable only by that account, SYSTEM and
Administrators. Both inherited and explicit grants are checked using security APIs.
Unsupported ACLs, junction/symlink credential paths, UNC paths and alternate data
streams are refused. `credentials` explains a rejection without changing any ACL.
Protecting an ordinary manually invoked Windows backup's ACL remains your responsibility.

## Guided setup and recovery

The interactive wizards help you choose safely while preserving the same verification and restore safeguards.


`snapark setup` guides repository/source selection, exclusions, profile creation,
optional initialization, a dry-run and a verified first backup. Existing profiles
and nonempty unrelated directories are never replaced. Passwords entered at its
hidden prompt stay in memory and reach restic through stdin. Keep a separate
password recovery plan; snapArk cannot recover a lost repository password.

### Backend installation

When restic is missing or incompatible, setup can offer the pinned official binary.
The download and destination require approval. Embedded SHA-256 checksums validate
the artifact; the executable's version is checked before publication. Installation
needs a local filesystem supporting hard links, refuses existing executables, saves
the upstream license, and never changes PATH. `setup --install-restic --dir PATH
--confirm` performs only that installation. Profiles remember the selected absolute
backend path; `--restic PATH` can select one explicitly. Schedules pin this path.

### Guided snapshot selection

`snapark recover` offers repository/profile selection, snapshot search by date, host,
label or ID, paged folder browsing, selection and a new destination. Numbers navigate;
`add N` selects a folder, `/text` filters a list, `n`/`p` page, and `q` cancels.
Single-source snapshots start at their source root; multi-source snapshots start at
`/`. Include paths in the preview are relative to that root. The full preflight
runs before approval; typing `restore` starts extraction and content verification.
A problematic entry anywhere in the chosen root can block selective recovery.
Choose a narrower root with scripted `restore --root` when appropriate.

### Scripted recovery

Interactive commands refuse background input. `snapshots`, `files`, `find`,
`restore --dry-run` and `restore` remain available for automation. On another
computer, use `recover --repo PATH` without the original configuration; all hosts
and historical revisions in that repository are visible. The wizard refuses a
destination inside known original sources and shares scripted restore safeguards.

## Maintaining local history

Archived history remains historical evidence; it is never treated as a newly completed check.


```sh
snapark history --id RECORD_ID --json
snapark history archive --before 2026-01-01T00:00:00Z --to /private/history-archive
snapark history archive --before 2026-01-01T00:00:00Z --to /private/history-archive --confirm
snapark history --archive /private/history-archive --json
snapark inspect-restore --target /path/to/incomplete-recovery --json
```

### Archive safeguards

Archival defaults to passed, finalized results older than the cutoff. Review failures
before adding `--include-failures`; unfinished `started` records always stay active.
New Windows history directories and archives use protected ACLs granting access
only to the current account, SYSTEM and Administrators; existing ACLs are never
changed automatically. Keep existing configuration/history directories private.
A new private archive receives synced, checksum-verified copies and a committed
manifest before originals are removed under the history lock. Unix directory-sync
failures abort before removal. Windows flushes individual files; power-loss
durability still depends on the filesystem. An interruption can
leave duplicates; incomplete archives without a manifest must be inspected while
retaining originals. Archives are never silently imported into active status or a
different repository identity. Local checksums detect accidental damage, not tampering.

### Diagnosing failed recovery

`inspect-restore` reads an incomplete marker and observes whether partial output
exists. It changes nothing and does not certify that output. Retry into a different
new destination after checking active processes. There is no automatic resume,
cleanup or lock removal. A backward wall-clock change leaves a finalized inconclusive
record instead of corrupting history or claiming a timed success.

### Export and import

Profile export/import is non-secret and deliberately remapped:

```sh
snapark profile export app > app-profile.json
snapark profile import --from app-profile.json --repo /mounted/backup --source /new/project replacement
```

Imports always get a fresh profile identity, start disabled, require explicit new
repository/source paths, and refuse an existing profile name. Review the imported
settings before using `profile replace` to enable it. Old-host snapshots remain
available through explicit `--repo` recovery; imports do not authorize retention
against their old profile scope. Exported paths can still be private metadata.
`profile add/replace --full-verify-hours N` enables an age expectation for successful
full-data checks. Zero leaves it disabled. Status reports consecutive observed
scheduled failures separately; lack of a run record is not evidence of why a job
failed to start. Check records retain their original scopes and do not become fresh
merely because a profile was copied or the repository path was reused.
