#!/usr/bin/env python3
"""Build standalone VEYRA archives and SHA256SUMS; never publish a release."""
import argparse
import datetime
import gzip
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


def archive_files(stage, name, archive, system, epoch):
    """Normalize metadata so equal source/toolchain builds have equal archives."""
    files = sorted(p for p in stage.rglob('*') if p.is_file())
    if system == 'windows':
        date = datetime.datetime.fromtimestamp(max(epoch, 315532800), datetime.timezone.utc)
        with zipfile.ZipFile(archive, 'w', compression=zipfile.ZIP_DEFLATED) as z:
            for file in files:
                info = zipfile.ZipInfo(f'{name}/{file.relative_to(stage).as_posix()}', date.timetuple()[:6])
                info.create_system = 3
                info.external_attr = 0o100644 << 16
                info.compress_type = zipfile.ZIP_DEFLATED
                z.writestr(info, file.read_bytes())
    else:
        with archive.open('wb') as raw, gzip.GzipFile(filename='', mode='wb', fileobj=raw, mtime=epoch) as compressed:
            with tarfile.open(fileobj=compressed, mode='w') as tar:
                for file in files:
                    info = tarfile.TarInfo(f'{name}/{file.relative_to(stage).as_posix()}')
                    info.size = file.stat().st_size
                    info.mtime = epoch
                    info.mode = 0o755 if file.name == 'veyra' else 0o644
                    with file.open('rb') as data:
                        tar.addfile(info, data)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, default=ROOT / "dist")
    parser.add_argument("--target", choices=TARGETS, action="append")
    args = parser.parse_args()
    args.out.mkdir(parents=True, exist_ok=True)
    checksums = []
    go = shutil.which("go")
    goroot = Path(subprocess.check_output([go, "env", "GOROOT"], text=True).strip())
    epoch = int(os.environ.get('SOURCE_DATE_EPOCH') or subprocess.check_output(
        ['git', 'show', '-s', '--format=%ct', 'HEAD'], cwd=ROOT, text=True).strip())
    for target in args.target or TARGETS:
        system, arch = target.split("-")
        name = f"veyra-{target}"
        with tempfile.TemporaryDirectory() as tmp:
            stage = Path(tmp) / name
            stage.mkdir()
            env = dict(os.environ, GOOS=system, GOARCH=arch, CGO_ENABLED="0")
            binary = stage / ("veyra.exe" if system == "windows" else "veyra")
            subprocess.run([go, "build", "-trimpath", "-buildvcs=true", "-o", str(binary), "./cmd/veyra"], cwd=ROOT, env=env, check=True)
            for file in ("THIRD-PARTY-LICENSES", "README.md", "SECURITY.md"):
                shutil.copyfile(ROOT / file, stage / file)
            shutil.copyfile(ROOT / "LICENSE.md", stage / "LICENSE")
            shutil.copytree(ROOT / "docs", stage / "docs")
            shutil.copyfile(goroot / "LICENSE", stage / "GO-LICENSE")
            shutil.copyfile(ROOT / "scripts/install-restic.py", stage / "install-restic.py")
            archive = args.out / (name + ('.zip' if system == 'windows' else '.tar.gz'))
            archive_files(stage, name, archive, system, epoch)
            digest = hashlib.sha256(archive.read_bytes()).hexdigest()
            checksums.append(f"{digest}  {archive.name}\n")
    (args.out / "SHA256SUMS").write_text("".join(checksums), encoding="ascii")
    print(f"Built {len(checksums)} archives with SHA256SUMS in {args.out}")


if __name__ == "__main__":
    main()
