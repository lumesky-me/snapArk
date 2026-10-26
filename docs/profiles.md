# Profiles and daily use

Profiles are version-1 JSON in the operating system's per-user configuration
directory under `veyra/profiles.json`. `--config /absolute/path/profiles.json`
selects another file. No passwords or commands are stored there. Password-file
paths remain explicit runtime arguments. Configuration inside a source or backup
repository is rejected; for a whole-home backup choose an external config location.

```sh
veyra profile add --repo /media/backup/app --source /home/me/app \
  --exclude node_modules --keep-last 10 --keep-daily 7 --keep-monthly 6 app
veyra profile default app
veyra profile list
veyra profile show app
veyra profile validate app
veyra plan --profile app --password-file /private/backup-password
veyra backup --profile app --password-file /private/backup-password
veyra status --password-file /private/backup-password
veyra doctor --profile app --password-file /private/backup-password
```

Options precede the profile name. Names use letters, digits, underscores, and
hyphens. Paths are resolved at creation, so profiles work from other directories.
`profile replace` takes a complete replacement configuration and preserves the
profile ID. Remove an installed schedule before deleting its profile.
`profile delete` removes only configuration. Neither deletes backups.
Use `--disabled` when adding/replacing to prevent profile backups and scheduling.
`--expected-hours 24` controls status warnings; zero disables freshness warnings.
`--verify metadata` (default) or `--verify full` checks the repository after backup.
A verification failure returns failure even if a snapshot was already stored.
Profile backup sources and exclusions cannot be overridden on the command line;
replace the profile configuration so status and retention keep their scope accurate.

Repeated `--include` settings select literal relative files/directories within
sources, not glob patterns. No include list means all source contents. Exclusion
patterns then apply using restic's rules. Missing include paths are ignored only
when other include paths exist; an entirely empty selection fails. Symlink
selections resolving outside a source are rejected. Explicit directory sources are
resolved to their physical roots, so a source-directory symlink or junction backs
up its contents rather than only the link. Internal links retain restic behavior.
Planning shows both configured and resolved source roots. Password files cannot be
inside any source. Profile checkpoints additionally exclude `.git` by default.

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
Status warns about newer pending work and a latest backup attempt without observed
completion, and retention never selects pending snapshots.
An interrupted completion step can leave a fully written snapshot pending; inspect
it with explicit `--repo`. Earlier unmarked profile snapshots remain recoverable
with `--repo` but are not retroactively certified as successful. JSON backup output
is a stream of restic events followed by a `completed` event carrying the final
snapshot ID; tagging changes that ID, so use the final completion ID for recovery. Unavailable repositories are not reported as empty.

`plan` opens repository metadata, authenticates with the supplied password source,
and runs restic's dry-run scanner without repository locks or writes. Counts are
estimates at scan time; deduplicated growth and full integrity are not established.
`doctor` uses shallow checks by default. `doctor --full` reads and authenticates
all packs explicitly. Available filesystem bytes are observed where supported, not predicted backup
capacity. Writer-lock health is unknown; diagnostics never remove locks.
Nonzero status/doctor results mean a problem or overdue/missing backup was observed.

Single-source profiles also work with `run --profile app -- COMMAND ARG...`.
The project defaults to that source; an explicit `--project` must match it.
Configured includes/exclusions apply to pre/post checkpoints: excluded data is not
protected. Multi-source profiles require an explicit repository/project workflow
instead. Protected-command checkpoints use the checkpoint scope and do not count
as ordinary profile backups for status or retention.

## Recovery and retention

```sh
veyra snapshots --profile app --password-file /private/backup-password
veyra find --profile app --query main.go --password-file /private/backup-password
veyra restore --profile app --target ./recovered --dry-run --password-file /private/backup-password latest
veyra restore --profile app --target ./recovered --include /src/main.go --password-file /private/backup-password latest
veyra retention --profile app --password-file /private/backup-password
veyra retention --profile app --confirm --password-file /private/backup-password
```

`find` searches literal substrings of original snapshot paths. It does not search
file contents. Exact snapshot IDs avoid ambiguity when recovering historical
profile revisions: use explicit `--repo` to inspect snapshots outside current scope.
Include-based snapshots may retain their absolute path hierarchy; inspect `files`
and use `restore --root /original/project` to choose the desired root. Single-file
snapshots restore into a new directory containing that file.

Restore previews create neither a destination nor a status marker. A real restore
reserves `<target>.veyra-incomplete.json` beside its destination before extraction.
Failure or interruption retains that marker and the partial target. Only a verified
successful restore removes it. A pre-existing marker blocks a new attempt until
it is inspected. Never treat a marked destination as a completed recovery.
An include pattern that matches nothing fails before creating either output.

Retention previews restic's keep/forget decisions, grouped by host and source
paths and limited to the selected profile scope. `--confirm` applies deletion.
Keep-last/daily/weekly/monthly/yearly policies combine using restic's keep rules.
Maintenance never runs during ordinary backup. It does not automatically prune;
`prune --repo ...` previews repository-wide unreferenced-data removal and requires
its own `--confirm`. Deleted historical recovery points may be unrecoverable after
pruning. Neither preview promises a future amount of reclaimed space. After an
interruption inspect snapshot history before retrying. Locks are never auto-removed.

## Scheduling

```sh
veyra credentials --password-file /absolute/private/password
veyra schedule preview --profile app --password-file /absolute/private/password
veyra schedule install --profile app --password-file /absolute/private/password --confirm
veyra schedule status --profile app --json
veyra schedule disable --profile app --confirm
veyra schedule enable --profile app --confirm
veyra schedule remove --profile app --confirm
```

Preview installs nothing. Other changes need `--confirm`; ordinary backup/setup
commands never install schedules. The interval is `expected-hours` (24 if zero),
limited to 31 days. Password-file paths are included, never password contents.
A disconnected drive or active backup makes a run fail safely. No retention runs.

- Linux uses user-systemd timers. A user manager must be available; VEYRA never
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

Status queries the native scheduler and distinguishes installation, enabled state,
loaded state, conflicts and inaccessible services. A loaded timer/job does not
prove that a backup ran. Ownership ties a definition to the profile and config
path; fingerprints detect edits. VEYRA refuses to change unrelated or edited
entries. Windows stores its native XML fingerprint beside the configuration in
`<config>.schedules/`; keep those ownership records with the configuration.
Existing macOS/systemd definitions are never overwritten: review, remove and
reinstall to update them. Older systemd definitions without a fingerprint require
manual review and removal before installation through the current CLI.

Schedules refer to absolute executable, configuration and password-file paths.
Keep those locations available; remove and reinstall a schedule when moving them.


Configuration writes use a private temporary file, sync, and atomic rename where
supported. Concurrent cooperative writers get a busy error instead of overwriting
each other. After a crash an orphan `.profiles-*` temp file is ignored. A stale
`profiles.json.lock` directory is never removed automatically: confirm no writer
is active before removing it manually. Malformed/unsupported versions remain
unchanged and must be restored or corrected before editing through the CLI.

## Verification history and recovery rehearsal

```sh
veyra verify --profile app --full --password-file /private/backup-password
veyra history --profile app --json
veyra test-restore --profile app --full --password-file /private/backup-password
veyra backup --profile app --scheduled --password-file /private/backup-password
```

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

`--scheduled` requires an absolute private password-file path, fails promptly if
another run owns the profile lock, and journals the attempt. Failure categories
separate credential rejection, unavailable/locked repositories, authentication,
source read errors and competing runs without storing raw errors. Task Scheduler
status also reports its native last result and run times; these are distinct from
repository verification. Scheduled execution never runs retention.
The pinned restic binary is found beside VEYRA before falling back to PATH. SIGTERM
and Ctrl-C request cancellation; ungraceful process termination can leave a started
record and lock for manual review. Windows unattended credentials must be local,
owned by the current account, and readable only by that account, SYSTEM and
Administrators. Both inherited and explicit grants are checked using security APIs.
Unsupported ACLs, junction/symlink credential paths, UNC paths and alternate data
streams are refused. `credentials` explains a rejection without changing any ACL.
Protecting an ordinary manually invoked Windows backup's ACL remains your responsibility.

## Guided setup and recovery

`veyra setup` guides repository/source selection, exclusions, profile creation,
optional initialization, a dry-run and a verified first backup. Existing profiles
and nonempty unrelated directories are never replaced. Passwords entered at its
hidden prompt stay in memory and reach restic through stdin. Keep a separate
password recovery plan; VEYRA cannot recover a lost repository password.

When restic is missing or incompatible, setup can offer the pinned official binary.
The download and destination require approval. Embedded SHA-256 checksums validate
the artifact; the executable's version is checked before publication. Installation
stages verified bytes in the destination directory, refuses existing executables,
and saves the upstream license. Windows publishes using a native rename without
replacement; Unix requires hard-link support for publication without replacement.
Unsupported filesystems fail rather than overwriting files. Installation never
changes PATH. `setup --install-restic --dir PATH
--confirm` performs only that installation. Profiles remember the selected absolute
backend path; `--restic PATH` can select one explicitly. Schedules pin this path.

`veyra recover` offers repository/profile selection, snapshot search by date, host,
label or ID, paged folder browsing, selection and a new destination. Numbers navigate;
`add N` selects a folder, `/text` filters a list, `n`/`p` page, and `q` cancels.
Single-source snapshots start at their source root; multi-source snapshots start at
`/`. Include paths in the preview are relative to that root. The full preflight
runs before approval; typing `restore` starts extraction and content verification.
A problematic entry anywhere in the chosen root can block selective recovery.
Choose a narrower root with scripted `restore --root` when appropriate.

Interactive commands refuse background input. `snapshots`, `files`, `find`,
`restore --dry-run` and `restore` remain available for automation. On another
computer, use `recover --repo PATH` without the original configuration; all hosts
and historical revisions in that repository are visible. The wizard refuses a
destination inside known original sources and shares scripted restore safeguards.

## Maintaining local history

```sh
veyra history --id RECORD_ID --json
veyra history archive --before 2026-01-01T00:00:00Z --to /private/history-archive
veyra history archive --before 2026-01-01T00:00:00Z --to /private/history-archive --confirm
veyra history --archive /private/history-archive --json
veyra inspect-restore --target /path/to/incomplete-recovery --json
```

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

`inspect-restore` reads an incomplete marker and observes whether partial output
exists. It changes nothing and does not certify that output. Retry into a different
new destination after checking active processes. There is no automatic resume,
cleanup or lock removal. A backward wall-clock change leaves a finalized inconclusive
record instead of corrupting history or claiming a timed success.

Profile export/import is non-secret and deliberately remapped:

```sh
veyra profile export app > app-profile.json
veyra profile import --from app-profile.json --repo /mounted/backup --source /new/project replacement
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
