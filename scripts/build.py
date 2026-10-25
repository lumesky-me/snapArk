#!/usr/bin/env python3
"""Build standalone VEYRA archives and SHA256SUMS; never publish a release."""
import argparse
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import zipfile

TARGETS = ("linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64")
ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, default=ROOT / "dist")
    parser.add_argument("--target", choices=TARGETS, action="append")
    args = parser.parse_args()
    args.out.mkdir(parents=True, exist_ok=True)
    checksums = []
    go = shutil.which("go")
    goroot = Path(subprocess.check_output([go, "env", "GOROOT"], text=True).strip())
    for target in args.target or TARGETS:
        system, arch = target.split("-")
        name = f"veyra-{target}"
        with tempfile.TemporaryDirectory() as tmp:
            stage = Path(tmp) / name
            stage.mkdir()
            env = dict(os.environ, GOOS=system, GOARCH=arch, CGO_ENABLED="0")
            binary = stage / ("veyra.exe" if system == "windows" else "veyra")
            subprocess.run([go, "build", "-trimpath", "-o", str(binary), "./cmd/veyra"], cwd=ROOT, env=env, check=True)
            for file in ("LICENSE", "README.md", "SECURITY.md"):
                shutil.copyfile(ROOT / file, stage / file)
            shutil.copytree(ROOT / "docs", stage / "docs")
            shutil.copyfile(goroot / "LICENSE", stage / "GO-LICENSE")
            shutil.copyfile(ROOT / "scripts/install-restic.py", stage / "install-restic.py")
            if system == "windows":
                archive = args.out / (name + ".zip")
                with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED) as z:
                    for file in sorted(stage.rglob("*")):
                        if file.is_file():
                            z.write(file, f"{name}/{file.relative_to(stage).as_posix()}")
            else:
                archive = args.out / (name + ".tar.gz")
                with tarfile.open(archive, "w:gz") as tar:
                    tar.add(stage, arcname=name)
            digest = hashlib.sha256(archive.read_bytes()).hexdigest()
            checksums.append(f"{digest}  {archive.name}\n")
    (args.out / "SHA256SUMS").write_text("".join(checksums), encoding="ascii")
    print(f"Built {len(checksums)} archives with SHA256SUMS in {args.out}")


if __name__ == "__main__":
    main()
