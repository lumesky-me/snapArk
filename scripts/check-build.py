#!/usr/bin/env python3
"""Verify build checksums and execute the native archived binary."""
import argparse
import hashlib
import json
from pathlib import Path
import platform
import subprocess
import tarfile
import tempfile
import zipfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dir", type=Path, default=Path("dist"))
    args = parser.parse_args()
    systems = {"Linux": "linux", "Darwin": "darwin", "Windows": "windows"}
    arches = {"x86_64": "amd64", "AMD64": "amd64", "arm64": "arm64", "aarch64": "arm64"}
    system = systems[platform.system()]
    target = f"veyra-{system}-{arches[platform.machine()]}"
    native = None
    for line in (args.dir / "SHA256SUMS").read_text(encoding="ascii").splitlines():
        expected, name = line.split()
        if Path(name).name != name:
            raise ValueError("invalid checksum filename")
        archive = args.dir / name
        actual = hashlib.sha256(archive.read_bytes()).hexdigest()
        if actual != expected:
            raise ValueError(f"checksum mismatch: {name}")
        if name in (target + ".zip", target + ".tar.gz"):
            native = archive
    if native is None:
        raise ValueError("no archive for this native platform")
    with tempfile.TemporaryDirectory(prefix="veyra package 日本語 ") as tmp:
        if native.suffix == ".zip":
            with zipfile.ZipFile(native) as z:
                z.extractall(tmp)
        else:
            with tarfile.open(native) as tar:
                tar.extractall(tmp, filter="data")
        root = Path(tmp) / target
        for name in ("LICENSE", "GO-LICENSE", "README.md", "SECURITY.md", "install-restic.py", "docs/profiles.md"):
            if not (root / name).is_file():
                raise ValueError(f"missing distribution file: {name}")
        binary = root / ("veyra.exe" if system == "windows" else "veyra")
        output = subprocess.check_output([str(binary), "version"], text=True)
        if not output.startswith("veyra ") or "requires restic 0.18.1" not in output:
            raise ValueError("unexpected standalone version output")
        # Exercise the packaged executable outside the checkout, using the pinned
        # backend installed by CI. No Go command is needed by this workflow.
        work = Path(tmp)
        source = work / "project with spaces 日本語"
        source.mkdir()
        (source / "file.txt").write_text("recover packaged data", encoding="utf-8")
        password = work / "password"
        password.write_text("disposable archive-test password", encoding="utf-8")
        password.chmod(0o600)
        repo = work / "backup with spaces"
        config = work / "settings with spaces" / "profiles.json"
        def run(*args):
            return subprocess.check_output([str(binary), *map(str, args)], cwd=work, text=True)
        run("profile", "add", "--config", config, "--repo", repo, "--source", source, "packaged")
        run("init", "--repo", repo, "--password-file", password)
        options = ["--config", config, "--profile", "packaged", "--password-file", password, "--json"]
        plan = json.loads(run("plan", *options))
        if plan["estimated_files"] != 1:
            raise ValueError("packaged backup plan did not find the source file")
        run("backup", *options)
        run("verify", *options, "--full")
        events = json.loads(run("history", *options))
        if not any(e["kind"] == "verification" and e["depth"] == "full-data" and e["outcome"] == "passed" for e in events):
            raise ValueError("packaged full verification was not recorded")
        sample = json.loads(run("test-restore", *options, "--sample-files", "1", "--max-bytes", "64", "--temp-dir", work))
        if sample["result"] != "passed" or len(sample["tested_files"]) != 1:
            raise ValueError("packaged recovery sample was not verified")
        rows = json.loads(run("status", *options))
        if len(rows) != 1 or rows[0]["state"] != "working":
            raise ValueError("packaged profile backup not confirmed")
        restored = work / "recovered"
        run("restore", *options, "--target", restored, "latest")
        if (restored / "file.txt").read_text(encoding="utf-8") != "recover packaged data":
            raise ValueError("packaged recovery contents differ")
        print("Packaged profiles, planning, backup, status, and recovery verified outside the checkout")
        print(output.strip())
        print("Archive checksums, distribution contents, and native executable verified")


if __name__ == "__main__":
    main()
