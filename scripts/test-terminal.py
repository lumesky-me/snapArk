#!/usr/bin/env python3
"""Exercise hidden password entry and actual guided recovery in a disposable PTY."""
import argparse
import errno
import os
from pathlib import Path
import platform
import select
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
            result, child_status = os.waitpid(pid, os.WNOHANG)
            if result:
                status = child_status
                break
        if status is None:
            result, status = os.waitpid(pid, os.WNOHANG)
            if not result:
                raise RuntimeError("interactive command did not finish before deadline")
        output = transcript.decode("utf-8", errors="replace")
        if secret in output:
            raise RuntimeError("hidden password appeared in terminal output")
        if status != expected_exit * 256 or index != len(answers):
            raise RuntimeError(f"interactive workflow failed (status {status}): {output}")
        return output
    finally:
        os.close(fd)
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
    with tempfile.TemporaryDirectory(prefix="veyra terminal ü ") as tmp:
        root = Path(tmp)
        source = root / "source"
        source.mkdir()
        (source / "important.txt").write_text("recover terminal data", encoding="utf-8")
        repo, cfg = root / "repository", root / "profiles.json"
        password = "disposable-hidden-terminal-password"
        drive(binary, ["setup", "--config", cfg], [
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
        print("Hidden password entry, cancellation, guided setup and recovery passed in a disposable terminal")


if __name__ == "__main__":
    main()
