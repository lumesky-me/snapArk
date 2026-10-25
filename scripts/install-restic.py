#!/usr/bin/env python3
"""Install the pinned restic release after validating its published SHA-256."""
import argparse
import bz2
import hashlib
import io
import os
from pathlib import Path
import platform
import tempfile
import urllib.request
import zipfile

VERSION = "0.18.1"


def artifact(system, machine):
    systems = {"Windows": "windows", "Darwin": "darwin", "Linux": "linux"}
    arches = {"AMD64": "amd64", "x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}
    if system not in systems or machine not in arches:
        raise ValueError(f"unsupported platform: {system}/{machine}")
    suffix = "zip" if system == "Windows" else "bz2"
    return f"restic_{VERSION}_{systems[system]}_{arches[machine]}.{suffix}"


def verified_payload(name, archive, manifest):
    matches = [line.split()[0] for line in manifest.splitlines()
               if len(line.split()) == 2 and line.split()[1] == name]
    if len(matches) != 1:
        raise ValueError(f"release checksum missing or ambiguous for {name}")
    if hashlib.sha256(archive).hexdigest() != matches[0]:
        raise ValueError("restic artifact checksum mismatch")
    if name.endswith(".zip"):
        with zipfile.ZipFile(io.BytesIO(archive)) as z:
            entries = [p for p in z.namelist() if p.endswith(".exe")]
            if len(entries) != 1 or Path(entries[0]).name != entries[0]:
                raise ValueError("unexpected Windows release archive")
            return z.read(entries[0])
    return bz2.decompress(archive)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dir", required=True, type=Path)
    args = parser.parse_args()
    name = artifact(platform.system(), platform.machine())
    base = f"https://github.com/restic/restic/releases/download/v{VERSION}/"
    with urllib.request.urlopen(base + "SHA256SUMS", timeout=60) as response:
        manifest = response.read().decode("ascii")
    with urllib.request.urlopen(base + name, timeout=120) as response:
        payload = verified_payload(name, response.read(), manifest)
    args.dir.mkdir(parents=True, exist_ok=True)
    target = args.dir / ("restic.exe" if platform.system() == "Windows" else "restic")
    fd, temp = tempfile.mkstemp(dir=args.dir)
    try:
        with os.fdopen(fd, "wb") as f:
            f.write(payload)
            f.flush()
            os.fsync(f.fileno())
        os.chmod(temp, 0o755)
        os.replace(temp, target)
    finally:
        if os.path.exists(temp):
            os.unlink(temp)
    print(f"Installed verified restic {VERSION}: {target}")


if __name__ == "__main__":
    main()
