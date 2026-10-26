# Security and recovery

snapArk is a local-first backup and recovery tool built around **restic 0.18.1**. It is still undergoing release validation and **has not had an independent security audit**.

> **Backups are not a substitute for a recovery plan.** Keep a separate copy of irreplaceable data, protect your repository password, and practice restoring files before you need them.

## At a glance

| Protection | What to know |
|---|---|
| **Encryption** | Restic encrypts and authenticates repository contents; snapArk does not implement its own encryption. |
| **Passwords** | Enter them through a hidden terminal prompt or use a private password file for automation. |
| **Recovery** | Restores use a new destination and verify restored contents; incomplete results are marked. |
| **Destructive actions** | Snapshot deletion and pruning require explicit confirmation. |
| **Limits** | Encryption cannot stop someone with filesystem access from deleting the repository, and no backup is immune to hardware failure. |

## Repository and password protection

Restic handles **encryption, authenticated storage, password derivation, locking, and repository integrity**.

- Losing every password or recovery key means **the encrypted data cannot be recovered**. Anyone with the correct key can decrypt it.
- Encryption does **not** conceal repository size or access patterns, nor does it prevent deletion by someone who can modify repository files.
- Restrict permissions on the repository and on any stored credentials. Keep backups on a separate device if you need protection from loss of the source disk.

### Entering passwords safely

Interactive password entry is hidden. Password bytes are passed to restic through standard input, not saved by snapArk or supplied as command-line arguments or environment variables. Inherited restic configuration is removed.

For unattended backups, supply a private, **regular** password file located outside every backed-up source directory. snapArk rejects symbolic-link password files and, on Unix, files accessible to the group or others (use permission mode 600).

**Windows unattended credentials** must be stored locally, owned by the current user, and readable only by that user, SYSTEM, and Administrators. Both inherited and explicit access-control entries are checked. Unsupported access entries, reparse or junction/symlink paths, UNC paths, alternate data streams, reserved device names, and ambiguous trailing dots or spaces are rejected by the release-preparation checks. Open handles restrict ordinary replacement during a scheduled run, but this does not defend against administrators, SYSTEM, or malicious software with the same user's privileges.

Run `snapark credentials --password-file PATH» to inspect eligibility without changing permissions or reading the password contents. **Protecting a password file used in an ordinary manual backup remains your responsibility.**

## Backup consistency and interruptions

snapArk streams file data rather than loading entire source trees into memory. Restic performs change detection and reports read failures.

> **A backup of files changing while they are read may not represent one consistent moment.** For databases and other applications with coordinated files, pause writes or use an application-specific export or snapshot mechanism first.

- Excluded files are never protected by that backup. Git history is not changed automatically.
- A failed backend process is reported as a failure, even when it left a partial snapshot.
- A disconnected destination drive must be mounted again at its expected path before retrying.
- An interrupted operation can leave a repository lock or unreferenced data. **Do not delete locks while another backup may still be running.** snapArk never automatically unlocks or repairs repositories; follow restic's recovery guidance after confirming all writers have stopped.

## Verification and deletion

There is a difference between having a backup and knowing its data is readable.

- `snapark verify» checks repository metadata and structure.
- `snapark verify --full» also reads and authenticates every stored data pack.
- Protected-command execution performs a full pre-operation repository check; failure blocks the command.

Verification results describe checks performed at a particular time; they cannot guarantee future recoverability. Pruning is not a substitute for verification.

Retention and snapshot deletion are separate from normal backups. `forget --confirm» removes snapshot references; a later `prune --confirm» can make that history permanently unrecoverable. Always preview the proposed changes first.

## Restore boundaries

**Restoration only targets a new directory**, beneath an existing, trusted parent. snapArk does not overwrite an existing destination.

Do not restore into an attacker-controlled parent or allow an untrusted process to change destination paths during extraction. snapArk does **not** defend against a local attacker racing filesystem operations under the same user account.

### What preflight checks

Before extraction, snapArk authenticates the selected snapshot data and checks for:

- directory traversal and absolute archive members;
- unsafe or escaping symbolic links and hard-link targets;
- unsupported special file or archive member types;
- names that cannot be used portably on the target filesystem; and
- duplicate, case-colliding, or canonically equivalent Unicode paths.

Unicode normalization uses `golang.org/x/text» for conservative collision detection. This may reject a snapshot even when a particular filesystem could distinguish its paths. Filesystem alias behavior has **not** been exhaustively tested. Some valid-but-nonportable archives may need expert inspection and recovery with restic directly.

Relative symbolic links are evaluated inside the chosen snapshot root, including on macOS systems with aliased temporary directories. Restic then extracts files and verifies their contents.

**A failed or interrupted restore remains incomplete.** Its output and status marker are retained for inspection; do not treat the directory as successfully recovered.

### Platform and metadata limitations

- File ownership, permissions, symbolic links, and other metadata can only be restored where the operating system and the user's privileges permit. Unsupported metadata or ownership errors are reported as failures.
- Windows symlink creation depends on privileges. Junctions and other reparse-point variants are not universally validated.
- On Windows, source scans reject unsupported reparse tags rather than silently following them. Known internal symlinks and junctions are not traversed during those scans. Checks can also reject problematic excluded entries.
- Unix paths and Windows drive paths are interpreted independently of the machine performing recovery. For snapshots originating on a Windows UNC path, inspect `snapark files» and supply an explicit `--root».
- Mount points, network shares, cloud-placeholder files, long paths, and filesystem-specific aliases have not all been certified. Keep the restore parent trusted.

Interactive and scripted recovery use the same authenticated preflight and verified extraction. Neither guarantees that unsupported metadata will transfer unchanged to another operating system.

## Protected commands are not a sandbox

When explicitly invoked through `snapark run», a command is preceded by a fully verified project checkpoint. Its environment filters inherited restic/snapArk variables and conventional password, token, secret, and API-key variables.

**The executed program is not sandboxed.** It can still read accessible files, use the network, and discover other credentials. Only commands explicitly wrapped through snapArk receive this checkpoint protection. snapArk does not watch every editor or agent action, and it never automatically rolls back files.

Cancellation requests terminate Unix process groups or use Windows taskkill for process trees. Processes that deliberately detach can escape those mechanisms; snapArk does not offer operating-system process containment.

---

**Using an existing build?** Until the executable rename is finished, substitute `veyra» for `snapark» in command examples. The underlying backup format remains restic's.

For setup instructions and day-to-day use, see [Profiles and daily use](docs/profiles.md).