#!/usr/bin/env python3
"""Run Go tests and preserve failures as readable GitHub annotations."""
import collections
import json
import subprocess
import sys


def main():
    process = subprocess.Popen(["go", "test", "-json", *sys.argv[1:]],
                               stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                               text=True, encoding="utf-8", errors="replace")
    output = collections.defaultdict(lambda: collections.deque(maxlen=24))
    for line in process.stdout:
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            print(line, end="", flush=True)
            continue
        key = (event.get("Package", ""), event.get("Test", ""))
        if event.get("Output"):
            text = event["Output"]
            print(text, end="", flush=True)
            output[key].append(text)
        if event.get("Action") == "fail":
            detail = (" / ".join(key) + "\n" + "".join(output[key]))[-7000:]
            detail = detail.replace("%", "%25").replace("\r", "%0D").replace("\n", "%0A")
            print("::error::" + detail, flush=True)
    return process.wait()


if __name__ == "__main__":
    sys.exit(main())
