#!/usr/bin/env python3
"""Check reachable vulnerabilities and preserve failures in CI annotations."""
import subprocess
import sys


def main():
    result = subprocess.run(['go', 'run', 'golang.org/x/vuln/cmd/govulncheck@v1.9.0', './...'],
                            capture_output=True, text=True)
    print(result.stdout, end='')
    print(result.stderr, end='', file=sys.stderr)
    if result.returncode:
        output = result.stdout + result.stderr
        detail = output if len(output) <= 14000 else output[:9000] + '\n... truncated ...\n' + output[-5000:]
        detail = detail.replace('%', '%25').replace('\r', '%0D').replace('\n', '%0A')
        print('::error::Go vulnerability check failed: ' + detail, flush=True)
    return result.returncode


if __name__ == '__main__':
    sys.exit(main())
