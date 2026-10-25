#!/usr/bin/env python3
"""Verify build checksums and execute the native archived binary."""
import argparse
import hashlib
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
    with tempfile.TemporaryDirectory() as tmp:
        if native.suffix == ".zip":
            with zipfile.ZipFile(native) as z:
                z.extractall(tmp)
        else:
            with tarfile.open(native) as tar:
                tar.extractall(tmp, filter="data")
        root = Path(tmp) / target
        for name in ("LICENSE", "GO-LICENSE", "README.md", "SECURITY.md", "install-restic.py"):
            if not (root / name).is_file():
                raise ValueError(f"missing distribution file: {name}")
        binary = root / ("veyra.exe" if system == "windows" else "veyra")
        output = subprocess.check_output([str(binary), "version"], text=True)
        if not output.startswith("veyra ") or "requires restic 0.18.1" not in output:
            raise ValueError("unexpected standalone version output")
        print(output.strip())
        print("Archive checksums, distribution contents, and native executable verified")


if __name__ == "__main__":
    main()
