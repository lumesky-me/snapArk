# Security and recovery

VEYRA is experimental and has not undergone an independent security audit.
Keep an independent backup of important data and practice restoring it.

## Repository protection

Restic 0.18.1 provides encryption, authenticated storage, password derivation,
locking, and repository integrity. Losing all passwords/keys makes recovery
impossible. Anyone with a key can decrypt the repository. Encryption does not
hide repository size or access patterns and does not prevent deletion by someone
with filesystem access. Keep repository and password permissions restricted.

Passwords may be entered through a hidden terminal prompt or read from a private
regular password file. VEYRA rejects symlink password files and Unix files with
group/other permissions. For unattended Windows use, the file must be owned by
the current account and grant access only to that account, SYSTEM, or
Administrators. Explicit and inherited grants are inspected with Windows security
APIs; unsupported ACEs fail closed. `veyra credentials --password-file PATH`
checks these rules without changing permissions or reading password contents.
Unattended credentials require a local path without reparse points, alternate
data streams, reserved device names or trailing dots/spaces. Open handles pin its components and prevent ordinary replacement
and writes for the duration of a scheduled run. This does not protect against
administrators, SYSTEM, or hostile processes running as the same account.
Password files inside a source are rejected. Inherited restic configuration is
removed; credentials are never accepted as password command arguments.

Wrapped commands receive a filtered environment without restic/VEYRA variables
or conventional token/password/secret/API-key variables. This is not a sandbox:
commands can read accessible files, access the network, and discover other
credentials. VEYRA executes only commands explicitly supplied by the user.
Protection applies only to operations wrapped with `veyra run`.

## Consistency and interrupted operations

Files are streamed rather than loaded in memory. Restic handles change detection
and reports read failures; nonzero backend status is a failure, even if a partial
snapshot exists. Backups are not atomic filesystem snapshots. Pause applications
or use application-specific exports for databases and other live multi-file data.
An excluded file is not protected. Git history is never changed automatically.

Restic's repository locks are retained. An interrupted process may leave a stale
lock or unreferenced data. Do not remove locks while another client may still be
working. Diagnose using restic's documented recovery tools after confirming all
writers have stopped. VEYRA does not automatically unlock or repair repositories.
A disconnected mounted drive must be remounted at the original path before retry.

`verify` checks repository structure and metadata; `verify --full` also reads and
authenticates every stored data pack. The protected-command gate uses the latter.
Failed verification blocks command execution. Pruning does not replace checking.
Deleting a snapshot with `forget --confirm` can make it unrecoverable after prune.

## Restore boundaries

Targets must be new directories with existing, trusted parents. Never restore
into an attacker-controlled directory or run restoration concurrently with an
untrusted process able to replace its paths. VEYRA is not designed to resist a
local attacker racing filesystem operations under the same account.

Preflight validates an authenticated streaming archive before extraction, rejecting
traversal, absolute or escaping symlinks, nonportable names, case collisions,
and unsupported special file types. Canonically equivalent Unicode names are
normalized with golang.org/x/text before case-alias comparison. This conservatively
rejects collisions even on filesystems that could keep both names. Filesystem-specific
alias behavior is not exhaustively tested. This is deliberately conservative: a valid
backup with such entries may require manual inspection and recovery using restic.
Relative symlinks are interpreted inside the selected snapshot root, independent
of macOS temporary-directory aliases. Restic performs extraction and content
verification. Failed restore directories are retained and must not be treated
as complete. Metadata is preserved only where the platform and privileges allow;
ownership errors and unsupported metadata are reported as failures.

Subprocess cancellation kills the process group on Unix and uses Windows taskkill
for the process tree. Programs can deliberately detach or escape those mechanisms;
VEYRA does not provide OS process containment. Ctrl-C cancels ongoing work.

Automatic source-root selection understands Unix paths and Windows drive paths
independently of the restore host. For UNC snapshots, inspect `veyra files` and
select an explicit `--root`. Windows symlink creation depends on OS privileges;
junctions and other reparse-point variants are not exhaustively validated.

Windows source scans refuse unsupported reparse tags before passing data to restic;
known internal symlinks and junctions are not traversed by that scan. Explicit source
roots still resolve to their physical locations. This metadata scan also checks
excluded entries and can conservatively reject a source requiring manual preparation.
Mount-point, remote-share, cloud-placeholder and long-path behavior is not universally
certified. Keep restore parents trusted, including when using directory aliases.

Interactive passwords are hidden and sent to restic through stdin, never persisted
or placed in arguments/environment. Interactive recovery performs the same authenticated
preflight and verified extraction as scripted restore. Neither path can guarantee
recovery of unsupported ownership, permissions, links or metadata on another OS.
