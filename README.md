<div align="center">

<h1>snapArk</h1>

**Your work has a past. Keep it.**

Encrypted backups, efficient snapshots, and reliable recovery tools for developers.

<p>
  <img alt="Go CLI" src="https://img.shields.io/badge/Go-CLI-0A7EA3?style=flat-square">
  <img alt="Windows, macOS, Linux" src="https://img.shields.io/badge/Windows%20%7C%20macOS%20%7C%20Linux-supported-334155?style=flat-square">
  <img alt="MIT License" src="https://img.shields.io/badge/license-MIT-64748b?style=flat-square">
</p>

**[Get started](#-quick-start) · [Explore features](#-what-you-can-do) · [Recovery](#-getting-your-files-back) · [Security](SECURITY.md)**

</div>

---

## 🛡️ A safety net for your files

A refactor goes wrong. A folder disappears. You realize yesterday's code was better.

**snapArk helps you get it back.**

snapArk is a **local-first, open-source command-line tool** for encrypted, versioned backups. It lets you protect folders and coding projects, keep historical snapshots, and recover earlier versions without relying on a cloud account.

Unlike copying a folder over and over, snapArk uses **incremental, deduplicated backups**: data that hasn't changed can be reused across snapshots. That makes keeping a history of your work much more space-efficient than storing full copies every time.

## ✨ What you can do

| | Capability | What it means for you |
|:---:|---|---|
| 🔐 | **Encrypted backups** | Protect backup contents with a password using restic's encryption. |
| 🕒 | **Snapshot history** | Browse previous backups and find the version you need. |
| 📦 | **Efficient storage** | Incremental backups reuse unchanged data instead of repeatedly storing it. |
| 🔄 | **Safe recovery** | Recover selected files, folders, or whole snapshots into a **new directory**. |
| 🗓️ | **Automatic scheduling** | Set up recurring backups through your operating system's native scheduler. |
| 🧩 | **Developer checkpoints** | Save project state before explicitly protected commands or risky edits. |
| ✅ | **Verification & restore tests** | Check backup integrity and rehearse recovery instead of assuming it works. |

### How snapshots work

Imagine you're building an app:

| Day | What happens | Your recovery options |
|---|---|---|
| **Monday** | You create your first backup. | Monday's project state |
| **Tuesday** | You change three files and back up again. | Monday or Tuesday |
| **Wednesday** | A refactor breaks the app. | Recover Tuesday's working files |

Each successful snapshot represents a point in time. **Unchanged data is shared where possible**, although new data still uses storage. Older recovery points remain available until removed by a retention or deletion operation.

> **snapArk is not a continuous file watcher.** Automatic backups run on the schedule you configure, so changes made since the last successful backup may not be protected yet.

## 🚀 Quick start

Start with the interactive setup, which guides you through choosing your files, a backup destination, and a first verified backup.

```sh
snapark setup
snapark backup --profile my-project
snapark recover --profile my-project
```

Use the profile name you created during setup. **The CLI rename is still in progress:** existing test builds currently use `veyra` in place of `snapark`.

**No official release has been published yet.** For now, test builds are available from successful [GitHub Actions runs](https://github.com/lumesky-me/veyra/actions). Choose a build for your platform, verify its included checksum, extract it, and run setup. GitHub may require sign-in to download artifacts.

You can also build from source using **Go 1.23+**. See the [source code](https://github.com/lumesky-me/veyra) and the [daily-use guide](docs/profiles.md) for detailed workflows and configuration.

## 🔄 Getting your files back

Backups are useful only when you can recover from them.

snapArk offers **guided recovery** to help you choose a repository, browse snapshots, select files or folders, and restore them to a new location. It also supports snapshot comparison, filename search, integrity checks, and actual recovery rehearsals.

Restores **do not overwrite your existing files**. If a restore fails or is interrupted, snapArk records that it was incomplete rather than treating the output as a successful recovery.

Developer checkpoints provide a separate way to preserve project state before running a command through snapArk. They do **not** automatically intercept every edit or roll back a failed command.

## 📅 Automatic backups, on your terms

Create a named backup profile, choose the folders to protect, and opt into a schedule:

- **Windows:** Task Scheduler, while the user is logged in.
- **macOS:** LaunchAgents in a user login session.
- **Linux:** systemd user timers.

Schedules require the destination to be available and properly secured credentials for unattended use. Missed backups are not guaranteed to run immediately when a computer wakes or reconnects. **Deletion, retention, and pruning aren't silently enabled** by ordinary scheduled backups.

See [profiles and daily use](docs/profiles.md) for setup, scheduling, exclusions, retention, and advanced commands.

## 🔐 Security and reliability

snapArk uses [restic](https://restic.net/) **0.18.1** for encrypted repository storage, content deduplication, and authenticated data handling. snapArk adds the developer-focused workflows around that backend; it does not invent its own encryption format.

- No cloud account is required. Store repositories on a local disk or mounted drive.
- Passwords are requested through a hidden terminal prompt or read from a suitably protected password file.
- Destructive snapshot deletion and pruning require explicit confirmation.
- Backup status, verification history, and restore rehearsals provide distinct signals; a recent backup alone does not guarantee recoverability.

**Important:** Keep your password safe, maintain another independent copy of irreplaceable data, and test recovery. Lost passwords cannot be recovered, a disconnected drive prevents backups, and some filesystem metadata may not restore identically across operating systems.

Read the [security and recovery limitations](SECURITY.md).

## Documentation

**[Profiles, scheduling & daily use](docs/profiles.md)** · **[Security & recovery](SECURITY.md)** · **[Builds & CI](https://github.com/lumesky-me/veyra/actions)**

## License

Released under the **MIT License**. See [LICENSE.md](LICENSE.md).

Copyright © 2026 gagan u.
