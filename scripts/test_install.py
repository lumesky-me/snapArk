import bz2
import hashlib
import io
import unittest
import zipfile

from importlib.util import spec_from_file_location, module_from_spec
from pathlib import Path

spec = spec_from_file_location("installer", Path(__file__).with_name("install-restic.py"))
installer = module_from_spec(spec)
spec.loader.exec_module(installer)


class InstallerTest(unittest.TestCase):
    def test_platform_artifacts(self):
        self.assertEqual(installer.artifact("Windows", "AMD64"), "restic_0.18.1_windows_amd64.zip")
        self.assertEqual(installer.artifact("Darwin", "arm64"), "restic_0.18.1_darwin_arm64.bz2")
        with self.assertRaises(ValueError):
            installer.artifact("Unknown", "mips")

    def test_checksum_and_windows_unpacking(self):
        name = installer.artifact("Windows", "AMD64")
        buf = io.BytesIO()
        with zipfile.ZipFile(buf, "w") as z:
            z.writestr("restic_0.18.1_windows_amd64.exe", b"test binary")
        data = buf.getvalue()
        manifest = hashlib.sha256(data).hexdigest() + "  " + name
        self.assertEqual(installer.verified_payload(name, data, manifest), b"test binary")
        with self.assertRaises(ValueError):
            installer.verified_payload(name, data + b"corrupted", manifest)
        with self.assertRaises(ValueError):
            installer.verified_payload(name, data, "")

    def test_unix_unpacking(self):
        name = installer.artifact("Linux", "x86_64")
        data = bz2.compress(b"test binary")
        manifest = hashlib.sha256(data).hexdigest() + "  " + name
        self.assertEqual(installer.verified_payload(name, data, manifest), b"test binary")


if __name__ == "__main__":
    unittest.main()
