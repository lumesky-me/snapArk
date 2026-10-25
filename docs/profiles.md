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
profile ID. `profile delete` removes only configuration. Neither deletes backups.
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
metadata; it does not store or invent a last-success flag. Verification history
is reported as unknown. Profile backups use a pending tag until backup and configured
verification succeed, then publish a completion tag using restic's own tag operation.
Status warns about newer pending work, and retention never selects pending snapshots.
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

Retention previews restic's keep/forget decisions, grouped by host and source
paths and limited to the selected profile scope. `--confirm` applies deletion.
Keep-last/daily/weekly/monthly/yearly policies combine using restic's keep rules.
Maintenance never runs during ordinary backup. It does not automatically prune;
`prune --repo ...` previews repository-wide unreferenced-data removal and requires
its own `--confirm`. Deleted historical recovery points may be unrecoverable after
pruning. Neither preview promises a future amount of reclaimed space. After an
interruption inspect snapshot history before retrying. Locks are never auto-removed.

## Scheduling previews

```sh
veyra schedule --profile app --password-file /private/backup-password --json
```

This prints definitions for the current OS and installs nothing. The interval is
`expected-hours` (24 hours if zero), limited to 31 days for scheduling. Choose a
password file with mode 600 on Unix or a restrictive Windows ACL. Definitions
contain its path only. Do not schedule with a password prompt. A disabled profile
fails before backup; missing drives fail safely until a later scheduled attempt.
No retention is scheduled. Review generated paths and the command before installing.

- Linux: save the service and timer strings as the indicated filenames under
  `~/.config/systemd/user/`, then run `systemctl --user daemon-reload` and
  `systemctl --user enable --now veyra-app.timer`. Disable with
  `systemctl --user disable --now veyra-app.timer`. The timer runs five minutes
  after user-manager startup and repeats while it is active. It is not a calendar
  timer and does not persist a record of missed runs.
- macOS: save the plist under `~/Library/LaunchAgents/`, then explicitly load it
  with `launchctl bootstrap gui/$(id -u) PATH_TO_PLIST`. Remove it with
  `launchctl bootout gui/$(id -u) PATH_TO_PLIST`. RunAtLoad requests a run at load;
  interval behavior depends on launchd and the login session.
- Windows: save the XML, inspect it, and import it in Task Scheduler. Select your
  user account and non-elevated interactive execution; review the trigger and
  StartWhenAvailable setting. Disable or delete the named task in Task Scheduler.
  Logged-out execution and machine-wide credentials are not configured by VEYRA.

These are previews, not tested scheduler installations. Native CI checks quoting
and XML generation without installing recurring tasks. No automatic updater is used.

Configuration writes use a private temporary file, sync, and atomic rename where
supported. Concurrent cooperative writers get a busy error instead of overwriting
each other. After a crash an orphan `.profiles-*` temp file is ignored. A stale
`profiles.json.lock` directory is never removed automatically: confirm no writer
is active before removing it manually. Malformed/unsupported versions remain
unchanged and must be restored or corrected before editing through the CLI.
