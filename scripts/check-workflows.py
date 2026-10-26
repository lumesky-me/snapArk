#!/usr/bin/env python3
"""Exercise historical recovery and measured incremental backups with a packaged CLI."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import random
import shutil
import subprocess
import sys
import tempfile
import time

from workflow_helpers import protect_windows_file


def manifest(root):
    files, directories = {}, []
    for entry in sorted(root.rglob('*')):
        name = entry.relative_to(root).as_posix()
        if entry.is_dir():
            directories.append(name)
        elif entry.is_file():
            files[name] = hashlib.sha256(entry.read_bytes()).hexdigest()
        else:
            raise RuntimeError('unexpected test fixture entry')
    return {'files': files, 'directories': directories}


def repository_bytes(repo):
    return sum(p.stat().st_size for p in repo.rglob('*') if p.is_file())



def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True, type=Path)
    parser.add_argument('--report', type=Path)
    parser.add_argument('--files', type=int, default=240)
    parser.add_argument('--binary-mib', type=int, default=8)
    parser.add_argument('--memory', action='store_true', help='measure peak child RSS (Linux only)')
    args = parser.parse_args()
    binary = args.binary.resolve()
    if args.files < 64 or args.binary_mib < 1:
        parser.error('fixtures need at least 64 files and 1 MiB binary contents')
    if args.memory and platform.system() != 'Linux':
        parser.error('--memory needs Linux resource accounting')
    measurements = {'platform': platform.platform(), 'source_files': args.files,
                    'binary_mib': args.binary_mib, 'operations_seconds': {}, 'repository_bytes': [], 'peak_rss_kib': {}}
    with tempfile.TemporaryDirectory(prefix='veyra regression 日本語 ') as tmp:
        work = Path(tmp)
        source, repo, cfg = work / 'project with spaces', work / 'repository', work / 'profiles.json'
        source.mkdir()
        password = work / 'private password'
        secret = 'disposable-release-regression-password'
        password.write_text(secret, encoding='utf-8')
        password.chmod(0o600)
        protect_windows_file(password)

        def run(command, *argv, expect=0, measure=None):
            started = time.monotonic()
            invocation = [str(binary), command, *map(str, argv)]
            memory_file = work / 'measurement.txt'
            if args.memory and measure:
                # A fresh accounting process per command avoids cumulative
                # RUSAGE_CHILDREN maxima hiding growth between operations.
                accounting = "import pathlib,resource,subprocess,sys; r=subprocess.run(sys.argv[2:]); pathlib.Path(sys.argv[1]).write_text(str(resource.getrusage(resource.RUSAGE_CHILDREN).ru_maxrss)); sys.exit(r.returncode)"
                invocation = [sys.executable, '-c', accounting, str(memory_file), *invocation]
            result = subprocess.run(invocation, cwd=work,
                                    capture_output=True, text=True, timeout=120)
            if secret in result.stdout + result.stderr:
                raise RuntimeError('credential appeared in command output')
            if measure:
                measurements['operations_seconds'][measure] = round(time.monotonic() - started, 4)
                if args.memory:
                    measurements['peak_rss_kib'][measure] = int(memory_file.read_text().strip())
            if result.returncode != expect:
                raise RuntimeError(f'{command}: exit {result.returncode}, expected {expect}: {result.stdout} {result.stderr}')
            return result.stdout

        # Incompressible data makes accidental full reuploads visible. Identical
        # copies exercise content reuse without promising an exact storage ratio.
        rng = random.Random(71237)
        large = rng.randbytes(args.binary_mib * 1024 * 1024)
        (source / 'assets').mkdir()
        (source / 'assets' / 'binary.dat').write_bytes(large)
        (source / 'assets' / 'same content.dat').write_bytes(large)
        for i in range(args.files):
            folder = source / 'src' / f'module-{i // 40}'
            folder.mkdir(parents=True, exist_ok=True)
            (folder / f'file-{i:03d}.go').write_text(f'package app\n// version A, file {i}\n', encoding='utf-8')
        (source / 'café 日本語.txt').write_text('Unicode recovery data', encoding='utf-8')
        (source / 'deleted later.txt').write_text('recover this from history', encoding='utf-8')
        (source / 'empty file').touch()
        (source / 'empty directory').mkdir()
        run('profile', 'add', '--config', cfg, '--repo', repo, '--source', source,
            '--keep-last', '2', '--expected-hours', '0', 'project')
        run('init', '--repo', repo, '--password-file', password)
        options = ['--config', cfg, '--profile', 'project', '--password-file', password]
        states, snapshots = [], []

        def backup(label):
            states.append(manifest(source))
            output = run('backup', *options, '--json', '--label', label, measure='backup_' + label)
            events = [json.loads(line) for line in output.splitlines()]
            completed = [e for e in events if e.get('message_type') == 'completed']
            if len(completed) != 1 or completed[0].get('verification') != 'metadata':
                raise RuntimeError('backup completion was not confirmed exactly once')
            snapshots.append(completed[0]['snapshot_id'])
            measurements['repository_bytes'].append(repository_bytes(repo))

        backup('A')
        # A truly unchanged backup must retain a new recovery point with almost
        # no stored growth. The generous bound covers encrypted metadata/packs.
        backup('unchanged')
        if measurements['repository_bytes'][1] - measurements['repository_bytes'][0] >= len(large) // 4:
            raise RuntimeError('unchanged backup unexpectedly stored a substantial copy of binary contents')
        changed = bytearray(large)
        changed[-4096:] = rng.randbytes(4096)
        (source / 'assets' / 'binary.dat').write_bytes(changed)
        for i in range(6):
            (source / 'src' / 'module-0' / f'file-{i:03d}.go').write_text(f'package app\n// version B, file {i}\n', encoding='utf-8')
        backup('B')
        (source / 'deleted later.txt').unlink()
        (source / 'new file.txt').write_text('version C addition', encoding='utf-8')
        backup('C')
        (source / 'src' / 'module-0' / 'file-000.go').write_text('intentionally broken source', encoding='utf-8')
        backup('D')
        run('plan', *options, '--json', measure='scan_plan')
        listed = json.loads(run('snapshots', *options, '--json', measure='snapshots'))
        if {s['id'] for s in listed} != set(snapshots):
            raise RuntimeError('historical snapshots were lost or completion IDs were wrong')

        # Deliberately remove the original source tree. Restore must depend on
        # authenticated repository data, not files left in the working project.
        shutil.rmtree(source)
        recovery_options = ['--repo', repo, '--password-file', password, '--config', cfg]
        for index, snapshot in enumerate(snapshots):
            target = work / f'historical-{index}'
            run('restore', *recovery_options, '--target', target, snapshot, measure='restore_' + str(index))
            if manifest(target) != states[index]:
                raise RuntimeError(f'historical recovery differs from expected version {index}')
        selected = work / 'selected B'
        run('restore', *recovery_options, '--target', selected, '--include', '/src/module-0/file-000.go', snapshots[2])
        recovered_files = manifest(selected)['files']
        expected = {'src/module-0/file-000.go': states[2]['files']['src/module-0/file-000.go']}
        if recovered_files != expected:
            raise RuntimeError('selective historical recovery selected the wrong data')
        deleted = work / 'deleted recovery'
        run('restore', *recovery_options, '--target', deleted, '--include', '/deleted later.txt', snapshots[2])
        if manifest(deleted)['files'] != {'deleted later.txt': states[2]['files']['deleted later.txt']}:
            raise RuntimeError('deleted file did not recover from its predecessor')
        directory = work / 'directory recovery'
        run('restore', *recovery_options, '--target', directory, '--include', '/src/module-1', snapshots[0])
        if manifest(directory)['files'] != {k: v for k, v in states[0]['files'].items() if k.startswith('src/module-1/')}:
            raise RuntimeError('directory selection differs from requested historical directory')
        missing = work / 'no matches'
        run('restore', *recovery_options, '--target', missing, '--include', '/missing', snapshots[0], expect=1)
        if missing.exists() or Path(str(missing) + '.veyra-incomplete.json').exists():
            raise RuntimeError('unmatched recovery created output')
        run('restore', *recovery_options, '--target', selected, snapshots[0], expect=1)
        wrong = work / 'wrong password'
        wrong.write_text('deliberately incorrect test credential', encoding='utf-8')
        wrong.chmod(0o600)
        protect_windows_file(wrong)
        run('snapshots', '--repo', repo, '--password-file', wrong, '--json', expect=1)
        run('snapshots', '--repo', repo, '--password-file', work / 'missing password', expect=1)
        run('verify', *options, '--full', measure='full_verification')
        run('test-restore', *recovery_options, '--sample-files', '2', '--max-bytes', '1024', '--temp-dir', work, snapshots[0])
        # Restore the source before freshness/source-boundary and scheduled-run checks.
        shutil.copytree(work / 'historical-4', source)
        status = json.loads(run('status', *options, '--json', measure='status'))
        if len(status) != 1 or status[0]['latest'] != snapshots[-1] or status[0]['state'] != 'working':
            raise RuntimeError('status did not identify the latest completed backup')
        before = {s['id'] for s in json.loads(run('snapshots', *options, '--json'))}
        run('retention', *options)
        run('forget', *recovery_options, snapshots[0])
        run('prune', *recovery_options)
        if {s['id'] for s in json.loads(run('snapshots', *options, '--json'))} != before:
            raise RuntimeError('a maintenance preview deleted snapshots')
        # Failure must be reflected in local run history without certifying a new backup.
        run('backup', '--config', cfg, '--profile', 'project', '--password-file', wrong, '--scheduled', expect=1)
        events = json.loads(run('history', '--config', cfg, '--profile', 'project', '--json'))
        if not any(e['kind'] == 'scheduled-run' and e['outcome'] == 'failed' for e in events):
            raise RuntimeError('scheduled authentication failure was not recorded')
        offline = work / 'disconnected repository'
        repo.rename(offline)
        run('backup', *options, '--scheduled', expect=1)
        offline.rename(repo)
        run('backup', *options, '--scheduled')
        run('status', *options, '--json')
        # Confirmed destructive operations remain confined to this disposable repo.
        run('forget', *recovery_options, '--confirm', snapshots[0])
        run('prune', *recovery_options, '--confirm')
        retained = work / 'after maintenance'
        run('restore', *recovery_options, '--target', retained, snapshots[-1])
        if manifest(retained) != states[-1]:
            raise RuntimeError('maintenance damaged retained historical data')
        if platform.system() == 'Linux':
            # Restrict only a new child and its disposable repository. EFBIG is
            # a controlled write failure, not a claim that real ENOSPC was tested.
            import resource
            limited_source = work / 'limited source'
            limited_source.mkdir()
            (limited_source / 'safe.txt').write_text('previous completed backup', encoding='utf-8')
            limited_repo, limited_cfg = work / 'limited repository', work / 'limited settings.json'
            run('profile', 'add', '--config', limited_cfg, '--repo', limited_repo, '--source', limited_source, 'limited')
            run('init', '--repo', limited_repo, '--password-file', password)
            limited_options = ['--config', limited_cfg, '--profile', 'limited', '--password-file', password, '--json']
            run('backup', *limited_options)
            good = {s['id'] for s in json.loads(run('snapshots', *limited_options))}
            (limited_source / 'new binary').write_bytes(rng.randbytes(8 * 1024 * 1024))

            def limit_file_writes():
                resource.setrlimit(resource.RLIMIT_FSIZE, (1024 * 1024, 1024 * 1024))

            failed = subprocess.run([str(binary), 'backup', *map(str, limited_options)], cwd=work,
                                    capture_output=True, text=True, timeout=90, preexec_fn=limit_file_writes)
            if failed.returncode == 0 or any(json.loads(line).get('message_type') == 'completed' for line in failed.stdout.splitlines()):
                raise RuntimeError('write-limited backup claimed completion')
            if {s['id'] for s in json.loads(run('snapshots', *limited_options))} != good:
                raise RuntimeError('write failure lost the previous completed snapshot or certified a partial backup')
            run('status', *limited_options, expect=1)
            print('Controlled Linux file-size write failure preserved the older backup and returned failure')
        print('Historical full/file/directory/deleted-file recovery, incremental reuse, failures and maintenance passed')
    if args.report:
        args.report.write_text(json.dumps(measurements, indent=2) + '\n', encoding='utf-8')
    print(json.dumps(measurements, sort_keys=True))


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        detail = str(error).replace('%', '%25').replace('\r', '%0D').replace('\n', '%0A')
        print('::error::Release workflow failed: ' + detail, flush=True)
        raise
