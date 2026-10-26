#!/usr/bin/env python3
"""Exercise hidden password entry and actual guided recovery in a disposable PTY."""
import argparse
import errno
import os
from pathlib import Path
import platform
import select
import shlex
import sys
import tempfile
import time


def drive(binary, args, answers, secret, expected_exit=0):
    import pty
    pid, fd = pty.fork()
    if pid == 0:
        os.execv(str(binary), [str(binary), *map(str, args)])
    transcript = bytearray()
    pending = bytearray()
    index = 0
    deadline = time.monotonic() + 120
    status = None
    reaped = False
    try:
        while time.monotonic() < deadline:
            readable, _, _ = select.select([fd], [], [], 0.25)
            if readable:
                try:
                    block = os.read(fd, 65536)
                except OSError as error:
                    if error.errno != errno.EIO:
                        raise
                    block = b""
                if block:
                    transcript.extend(block)
                    pending.extend(block)
                    if index < len(answers):
                        prompt, reply = answers[index]
                        if prompt.encode() in pending:
                            # Every prompt must arrive before its corresponding input;
                            # password input is never queued while terminal echo is on.
                            time.sleep(0.05)
                            os.write(fd, reply.encode() + b"\n")
                            pending.clear()
                            index += 1
                else:
                    break
            if not reaped:
                result, child_status = os.waitpid(pid, os.WNOHANG)
                if result:
                    status = child_status
                    reaped = True
                    # A fast child may exit before its final terminal output is
                    # read. Drain the PTY to EOF before assessing its report.
        # On macOS the slave can reach EOF a little before the child becomes
        # waitable. EOF is not a timeout; keep the same bounded exit deadline.
        while not reaped and time.monotonic() < deadline:
            result, child_status = os.waitpid(pid, os.WNOHANG)
            if result:
                status = child_status
                reaped = True
            else:
                time.sleep(0.01)
        if not reaped:
            raise RuntimeError("interactive command did not finish before deadline")
        output = transcript.decode("utf-8", errors="replace")
        if secret in output:
            raise RuntimeError("hidden password appeared in terminal output")
        if status != expected_exit * 256 or index != len(answers):
            raise RuntimeError(f"interactive workflow failed (status {status}): {output}")
        return output
    finally:
        os.close(fd)
        if not reaped:
            try:
                os.kill(pid, 9)
            except ProcessLookupError:
                pass
            try:
                os.waitpid(pid, 0)
            except ChildProcessError:
                pass


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    args = parser.parse_args()
    if platform.system() == "Windows":
        print("PTY workflow uses Unix terminals; Windows controlled-input workflows run in Go tests")
        return
    binary = args.binary.resolve()
    # Exercise the observed EOF-before-exit case deterministically, alongside
    # real terminal workflows below. Closing descriptors must not lose output
    # or turn an ordinary process exit into a false timeout.
    final = drive(Path(sys.executable), ["-c", "import os,time; print('final report',flush=True); "
                  "os.close(0); os.close(1); os.close(2); time.sleep(0.05)"], [], "unused secret")
    if "final report" not in final:
        raise RuntimeError("terminal output was lost at process exit")
    with tempfile.TemporaryDirectory(prefix="veyra terminal ü ") as tmp:
        root = Path(tmp)
        source = root / "source"
        source.mkdir()
        (source / "important.txt").write_text("recover terminal data", encoding="utf-8")
        repo, cfg = root / "repository", root / "settings $ ' 日本語.json"
        password = "disposable-hidden-terminal-password"
        setup_output = drive(binary, ["setup", "--config", cfg], [
            ("Name this backup profile", "terminal"),
            ("Backup repository path", str(repo)),
            ("Source directory:", str(source)),
            ("Another source directory", ""),
            ("Exclude pattern", ""),
            ("Repository password (hidden):", password),
            ("Repository password (hidden):", password),
            ("Initialize and save?", "y"),
            ("Preview the initial backup", "y"),
            ("Create the initial backup", "y"),
        ], password)
        # Printed commands must work without PATH changes and preserve literal
        # spaces, Unicode, quotes and shell expansion characters in config paths.
        recovery_command = next(line.removeprefix("Recover: ") for line in setup_output.splitlines()
                                if line.startswith("Recover: "))
        if shlex.split(recovery_command) != [str(binary), "recover", "--config", str(cfg), "--profile", "terminal"]:
            raise RuntimeError("setup printed an unsafe or unusable recovery command")
        recovered = root / "recovered"
        output = drive(binary, ["recover", "--repo", repo], [
            ("Repository password (hidden):", password),
            ("Snapshot number", "1"),
            ("Number opens a folder", "all"),
            ("New destination directory", str(recovered)),
            ("Type restore", "restore"),
        ], password)
        if "Recovery completed and file contents verified" not in output:
            raise RuntimeError("no verified recovery report")
        if (recovered / "important.txt").read_text(encoding="utf-8") != "recover terminal data":
            raise RuntimeError("guided terminal recovery contents differ")
        drive(binary, ["recover", "--repo", repo], [
            ("Repository password (hidden):", "\x03"),
        ], password, expected_exit=1)
        # An explicit but unusable password file must fail even with a live
        # controlling terminal; unattended execution must never prompt there.
        blank = root / "blank password"
        blank.write_text("  \n", encoding="utf-8")
        blank.chmod(0o600)
        failed = drive(binary, ["backup", "--scheduled", "--config", cfg,
                               "--profile", "terminal", "--password-file", blank],
                       [], password, expected_exit=1)
        if "enter password for repository" in failed.lower():
            raise RuntimeError("explicit password file fell back to a terminal prompt")
        print("Hidden password entry, cancellation, guided setup and recovery passed in a disposable terminal")


if __name__ == "__main__":
    main()
