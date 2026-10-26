#!/usr/bin/env python3
"""Verify build checksums and execute the native archived binary."""
import argparse
import datetime
import hashlib
import json
import os
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
    script_root = Path(__file__).resolve().parent
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
        for name in ("LICENSE", "THIRD-PARTY-LICENSES", "GO-LICENSE", "README.md", "SECURITY.md", "install-restic.py", "docs/profiles.md"):
            if not (root / name).is_file():
                raise ValueError(f"missing distribution file: {name}")
        binary = root / ("veyra.exe" if system == "windows" else "veyra")
        output = subprocess.check_output([str(binary), "version"], text=True)
        if not output.startswith("veyra ") or "requires restic 0.18.1" not in output:
            raise ValueError("unexpected standalone version output")
        build = json.loads(subprocess.check_output([str(binary), "version", "--json"], text=True))
        if build["os"] != system or build["arch"] != arches[platform.machine()] or len(build["revision"]) != 40:
            raise ValueError("archive build provenance/platform is missing or incorrect")
        expected_revision = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=script_root.parent, text=True).strip()
        if build["revision"] != expected_revision:
            raise ValueError("archive revision differs from source checkout")
        if os.environ.get('CI') == 'true' and build['modified']:
            raise ValueError('CI archive was built from modified sources')
        rebuilt = Path(tmp) / 'rebuilt'
        import sys
        subprocess.run([sys.executable, str(script_root / 'build.py'), '--target',
                        f'{system}-{arches[platform.machine()]}', '--out', str(rebuilt)], check=True)
        if hashlib.sha256((rebuilt / native.name).read_bytes()).digest() != hashlib.sha256(native.read_bytes()).digest():
            raise ValueError('repeated build with the same sources/toolchain produced a different archive')
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
            result = subprocess.run([str(binary), *map(str, args)], cwd=work, text=True,
                                    capture_output=True)
            if result.returncode:
                raise RuntimeError(f"{args[0]} failed ({result.returncode}): {result.stdout} {result.stderr}")
            return result.stdout
        # Exercise the compiled installer itself, with explicit download approval.
        installed = json.loads(run("setup", "--install-restic", "--dir", root, "--confirm", "--json"))
        if not Path(installed["installed"]).is_file() or not (root / "RESTIC-0.18.1-LICENSE").is_file():
            raise ValueError("compiled backend installation or license missing")
        if installed["version"] != "0.18.1":
            raise ValueError("unexpected installed backend version")
        terminal = subprocess.run([sys.executable, str(script_root / "test-terminal.py"), "--binary", str(binary)],
                                  cwd=work, capture_output=True, text=True, timeout=180)
        if terminal.returncode:
            raise RuntimeError("terminal workflow failed: " + terminal.stdout + terminal.stderr)
        print(terminal.stdout, end="")
        for command in ("recover", "setup"):
            result = subprocess.run([str(binary), command], cwd=work, input="", text=True,
                                    capture_output=True, timeout=20)
            if result.returncode != 1 or "interactive terminal" not in result.stderr:
                raise ValueError("interactive command did not refuse background execution")
        run("profile", "add", "--config", config, "--repo", repo, "--source", source,
            "--restic", installed["installed"], "packaged")
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
        archive = work / "history archive ü"
        cutoff = (datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(hours=1)).isoformat()
        archived = json.loads(run("history", "archive", "--config", config, "--before", cutoff,
                                  "--to", archive, "--confirm", "--json"))
        if archived["records"] < 1:
            raise ValueError("packaged history archival saved no records")
        saved = json.loads(run("history", "--archive", archive, "--json"))
        if not any(e["kind"] == "verification" and e["outcome"] == "passed" for e in saved):
            raise ValueError("packaged history archive did not preserve verification records")
        regression = subprocess.run([sys.executable, str(script_root / "check-workflows.py"),
                                     "--binary", str(binary)], cwd=work, capture_output=True,
                                    text=True, timeout=600)
        if regression.returncode:
            raise RuntimeError("historical/incremental workflow failed: " + regression.stdout + regression.stderr)
        print(regression.stdout, end="")
        scheduled = subprocess.run([sys.executable, str(script_root / "test-scheduled-backup.py"),
                                    "--binary", str(binary)], cwd=work, capture_output=True,
                                   text=True, timeout=300)
        if scheduled.returncode:
            raise RuntimeError("native scheduled backup failed: " + scheduled.stdout + scheduled.stderr)
        print(scheduled.stdout, end="")
        print("Packaged installation, profiles, backup, recovery and history verified outside the checkout")
        print(output.strip())
        print("Archive checksums, distribution contents, and native executable verified")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        message = str(error).replace("%", "%25").replace("\r", "%0D").replace("\n", "%0A")
        print("::error::Packaged workflow failed: " + message, flush=True)
        raise
