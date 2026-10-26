#!/usr/bin/env python3
"""Check real pre-rename profiles, snapshots, journals, markers and schedules."""
import argparse
import datetime
import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile

from workflow_helpers import protect_windows_file

ROOT = Path(__file__).resolve().parents[1]
# Preserved pre-rename source tree from the successful five-platform run.
LEGACY_REVISION = 'f8347db0f8bcbaf088903605469958e630fc0f27'


def legacy_binary(work):
    source = work / 'legacy source'
    source.mkdir()
    archive = subprocess.check_output(['git', 'archive', LEGACY_REVISION, 'go.mod', 'go.sum', 'cmd', 'internal'], cwd=ROOT)
    with tarfile.open(fileobj=io.BytesIO(archive)) as tar:
        tar.extractall(source, filter='data')
    binary = work / ('veyra.exe' if os.name == 'nt' else 'veyra')
    # This is an isolated source fixture from a pinned commit, not a release
    # artifact. No checkout, documentation file or branch is modified.
    subprocess.run(['go', 'build', '-trimpath', '-buildvcs=false', '-o', str(binary), './cmd/veyra'],
                   cwd=source, env=dict(os.environ, GOWORK='off'), check=True, timeout=180)
    return binary


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--legacy-binary', type=Path)
    args = parser.parse_args()
    current = args.binary.resolve()
    with tempfile.TemporaryDirectory(prefix='snapark upgrade 日本語 ') as tmp:
        work = Path(tmp).resolve()
        legacy = args.legacy_binary.resolve() if args.legacy_binary else legacy_binary(work)
        source, repo, cfg = work / 'project with spaces', work / 'repository', work / 'profiles.json'
        source.mkdir()
        original = b'previous installation, original contents\n'
        file = source / 'café 日本語.txt'
        file.write_bytes(original)
        password = work / 'private password'
        password.write_text('disposable-upgrade-password', encoding='utf-8')
        password.chmod(0o600)
        protect_windows_file(password)

        def run(binary, command, *argv, expect=0):
            result = subprocess.run([str(binary), command, *map(str, argv)], cwd=work,
                                    capture_output=True, text=True, timeout=120)
            if result.returncode != expect:
                raise RuntimeError(f'{command}: exit {result.returncode}, expected {expect}: {result.stdout} {result.stderr}')
            return result.stdout

        run(legacy, 'profile', 'add', '--config', cfg, '--source', source, '--repo', repo, 'old-project')
        run(legacy, 'init', '--repo', repo, '--password-file', password)
        options = ['--config', cfg, '--profile', 'old-project', '--password-file', password, '--json']
        output = run(legacy, 'backup', *options)
        completed = [json.loads(line) for line in output.splitlines() if json.loads(line).get('message_type') == 'completed']
        old_snapshot = completed[0]['snapshot_id']
        run(legacy, 'checkpoint', *options)
        config_before = cfg.read_bytes()
        status = json.loads(run(current, 'status', *options))
        if status[0]['latest'] != old_snapshot or cfg.read_bytes() != config_before:
            raise RuntimeError('old profile was hidden, changed or assigned a different backup')
        checkpoints = json.loads(run(current, 'checkpoints', *options))
        if len(checkpoints) != 1:
            raise RuntimeError('old checkpoint scope is not readable')
        file.write_text('new contents from snapArk', encoding='utf-8')
        run(current, 'backup', *options)
        # Compatibility is bidirectional: existing automation can inspect and
        # recover snapshots created after installing the new command.
        listed = json.loads(run(legacy, 'snapshots', *options))
        if len(listed) != 2 or old_snapshot not in {s['id'] for s in listed}:
            raise RuntimeError('legacy CLI lost old or new profile snapshots')
        newest = json.loads(run(current, 'status', *options))[0]['latest']
        legacy_target = work / 'legacy recovery'
        run(legacy, 'restore', *options, '--target', legacy_target, newest)
        if (legacy_target / file.name).read_bytes() != file.read_bytes():
            raise RuntimeError('legacy CLI cannot recover the new snapshot')
        shutil.rmtree(source)
        target = work / 'separate machine recovery'
        recovery = ['--repo', repo, '--password-file', password, '--config', work / 'fresh configuration.json']
        run(current, 'restore', *recovery, '--target', target, old_snapshot)
        if hashlib.sha256((target / file.name).read_bytes()).digest() != hashlib.sha256(original).digest():
            raise RuntimeError('historical recovery requires the old source or configuration')
        # Old version-zero markers must still block reuse and remain readable.
        incomplete = work / 'old incomplete recovery'
        marker = Path(str(incomplete) + '.veyra-incomplete.json')
        marker.write_text(json.dumps({'state': 'incomplete', 'snapshot': old_snapshot,
                                      'target': str(incomplete)}), encoding='utf-8')
        before = marker.read_bytes()
        inspected = json.loads(run(current, 'inspect-restore', '--target', incomplete, '--json'))
        if inspected['attempt']['snapshot'] != old_snapshot or inspected['output'] != 'missing':
            raise RuntimeError('legacy incomplete recovery marker is not understood')
        run(current, 'restore', *recovery, '--target', incomplete, old_snapshot, expect=1)
        if marker.read_bytes() != before or incomplete.exists():
            raise RuntimeError('legacy recovery marker was replaced or ignored')
        events = json.loads(run(legacy, 'history', '--config', cfg, '--json'))
        if not any(e['kind'] == 'backup' and e['outcome'] == 'passed' for e in events):
            raise RuntimeError('legacy CLI cannot read current journal records')
        archived = work / 'old history archive'
        cutoff = (datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(hours=1)).isoformat()
        run(legacy, 'history', 'archive', '--config', cfg, '--before', cutoff, '--to', archived, '--confirm', '--json')
        archive_before = {p.name: p.read_bytes() for p in archived.iterdir() if p.is_file()}
        history = json.loads(run(current, 'history', '--config', cfg, '--archive', archived, '--json'))
        if not history or archive_before != {p.name: p.read_bytes() for p in archived.iterdir() if p.is_file()}:
            raise RuntimeError('legacy archive was unreadable or changed during inspection')
        if cfg.read_bytes() != config_before:
            raise RuntimeError('upgrade silently changed existing configuration')
        print('Legacy profiles, bidirectional backups/recovery, checkpoints, journals, archives and incomplete markers passed')
        scheduled = subprocess.run([sys.executable, str(ROOT / 'scripts/test-scheduled-backup.py'),
                                    '--binary', str(current), '--legacy-binary', str(legacy)],
                                   cwd=work, capture_output=True, text=True, timeout=300)
        if scheduled.returncode:
            raise RuntimeError('legacy native schedule failed: ' + scheduled.stdout + scheduled.stderr)
        print(scheduled.stdout, end='')


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        detail = str(error).replace('%', '%25').replace('\r', '%0D').replace('\n', '%0A')
        print('::error::Upgrade workflow failed: ' + detail, flush=True)
        raise
