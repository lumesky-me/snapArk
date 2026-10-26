#!/usr/bin/env python3
"""Run a disposable packaged backup through the current user's native scheduler."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess
import tempfile
import time
import uuid

from workflow_helpers import protect_windows_file

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    args = parser.parse_args()
    if os.environ.get('VEYRA_NATIVE_SCHEDULER_TESTS') != '1':
        print('::notice::SKIP native backup execution: set VEYRA_NATIVE_SCHEDULER_TESTS=1 to opt in')
        return
    system = platform.system()
    if system == 'Linux':
        # Service-launched CI workers may omit the login environment even when
        # this account already has a private user bus. Reuse only that existing
        # bus; never start a manager, enable lingering, or elevate privileges.
        runtime = Path(f'/run/user/{os.getuid()}')
        if not os.environ.get('XDG_RUNTIME_DIR') and runtime.exists():
            info = runtime.stat()
            if info.st_uid == os.getuid() and info.st_mode & 0o077 == 0 and (runtime / 'bus').is_socket():
                os.environ['XDG_RUNTIME_DIR'] = str(runtime)
        probe = subprocess.run(['systemctl', '--user', 'show-environment'], capture_output=True, text=True)
        if probe.returncode:
            print('::notice::SKIP native systemd backup execution: no accessible user manager: ' + probe.stderr.strip())
            return
    elif system == 'Darwin':
        subprocess.run(['/bin/launchctl', 'print', f'gui/{os.getuid()}'], check=True, capture_output=True)
    binary = args.binary.resolve()
    with tempfile.TemporaryDirectory(prefix='veyra schedule 日本語 ') as tmp:
        work = Path(tmp)
        source, repo, cfg = work / 'source with spaces', work / 'repository', work / 'settings.json'
        source.mkdir()
        (source / 'file.txt').write_text('native scheduled recovery', encoding='utf-8')
        password = work / 'private password'
        password.write_text('disposable-native-schedule-password', encoding='utf-8')
        password.chmod(0o600)
        protect_windows_file(password)
        name = 'test-' + uuid.uuid4().hex[:12]

        def run(command, *argv, expect=0):
            result = subprocess.run([str(binary), command, *map(str, argv)], cwd=work,
                                    capture_output=True, text=True, timeout=60)
            if result.returncode != expect:
                raise RuntimeError(f'{command}: exit {result.returncode}, expected {expect}: {result.stdout} {result.stderr}')
            return result.stdout

        run('profile', 'add', '--config', cfg, '--repo', repo, '--source', source, name)
        run('init', '--repo', repo, '--password-file', password)
        options = ['--config', cfg, '--profile', name, '--password-file', password, '--json']
        digest = hashlib.sha256(str(cfg).encode('utf-8')).hexdigest()
        native_name = 'veyra-' + name + '-' + digest[:12]
        profile = json.loads(cfg.read_text(encoding='utf-8'))['profiles'][name]
        owner = profile['id'] + ':' + digest

        def schedule(action, expect=0):
            return json.loads(run('schedule', *options, '--confirm', action, expect=expect) or '{}')

        def history():
            return json.loads(run('history', '--config', cfg, '--profile', name, '--json'))

        def await_outcome(outcome, previous):
            deadline = time.monotonic() + 60
            while time.monotonic() < deadline:
                events = [e for e in history() if e['kind'] == 'scheduled-run' and e['id'] not in previous]
                finished = [e for e in events if e['outcome'] != 'started']
                if finished:
                    if finished[-1]['outcome'] != outcome:
                        raise RuntimeError(f'native scheduled backup had unexpected outcome: {finished[-1]}')
                    return
                time.sleep(0.25)
            detail = json.dumps(history()[-6:], sort_keys=True)
            if system == 'Linux':
                state = subprocess.run(['systemctl', '--user', 'status', '--no-pager', f'veyra-{name}.service'], capture_output=True, text=True)
                detail += '\n' + state.stdout + state.stderr
            raise RuntimeError('native scheduled backup did not record completion within 60 seconds: ' + detail)

        def windows_action(action):
            # Only this fixture's randomly named and owner-matched task is run
            # or stopped. Values travel over stdin, not executable shell text.
            script = r'''$ErrorActionPreference='Stop'
[Console]::InputEncoding=New-Object System.Text.UTF8Encoding($false)
$r=[Console]::In.ReadToEnd()|ConvertFrom-Json
$s=New-Object -ComObject 'Schedule.Service'; $s.Connect()
$t=$s.GetFolder('\').GetTask([string]$r.name)
if(-not $t.Xml.Contains('VEYRA owner='+[string]$r.owner)){throw 'Owner mismatch'}
if($r.action -eq 'run'){ $null=$t.Run($null) }
elseif($r.action -eq 'stop'){ if($t.State -eq 4){ $t.Stop(0) } }
else{throw 'Unknown operation'}
'''
            subprocess.run(['powershell.exe', '-NoProfile', '-NonInteractive', '-Command', script],
                           input=json.dumps({'name': native_name, 'owner': owner, 'action': action}),
                           text=True, capture_output=True, check=True, timeout=30)

        def trigger(outcome='passed'):
            if system == 'Windows':
                windows_action('run')
            elif system == 'Darwin':
                subprocess.run(['/bin/launchctl', 'kickstart', f'gui/{os.getuid()}/me.lumesky.{native_name}'], check=True, capture_output=True)
            else:
                result = subprocess.run(['systemctl', '--user', 'start', f'veyra-{name}.service'], capture_output=True, text=True)
                # Type=oneshot start waits for the backup and propagates its
                # expected failure. The journal below must independently agree.
                if result.returncode != 0 and not (outcome == 'failed' and result.returncode == 1):
                    raise RuntimeError(f'systemd start failed ({result.returncode}): {result.stdout} {result.stderr}')

        installed = False
        try:
            run('schedule', *options, 'preview')
            if schedule('status')['state'] != 'missing':
                raise RuntimeError('random disposable schedule unexpectedly exists')
            installed = True
            schedule('install')
            if schedule('status')['state'] != 'enabled':
                raise RuntimeError('installed schedule is not enabled')
            schedule('install', expect=1)
            # launchd RunAtLoad starts immediately; other platforms are manually
            # triggered through their native scheduler without changing cadence.
            if system != 'Darwin':
                trigger()
            await_outcome('passed', set())
            schedule('disable')
            if schedule('status')['state'] != 'disabled':
                raise RuntimeError('native schedule did not disable')
            before_enable = {e['id'] for e in history()}
            schedule('enable')
            if schedule('status')['state'] != 'enabled':
                raise RuntimeError('native schedule did not enable')
            # Enabling a LaunchAgent can start another RunAtLoad. Wait for it
            # before disconnecting storage and deliberately injecting failure.
            if system == 'Darwin':
                await_outcome('passed', before_enable)
            elif system == 'Linux':
                # An already elapsed OnBootSec can trigger a run when the timer
                # is enabled. Wait for the oneshot service before disconnecting
                # storage, so its event cannot be mistaken for an older run.
                trigger()
                await_outcome('passed', before_enable)
            previous = {e['id'] for e in history()}
            offline = work / 'disconnected'
            repo.rename(offline)
            try:
                trigger('failed')
                await_outcome('failed', previous)
            finally:
                offline.rename(repo)
            run('status', *options, expect=1)
            previous = {e['id'] for e in history()}
            trigger()
            await_outcome('passed', previous)
            status = json.loads(run('status', *options))
            target = work / 'recovered'
            run('restore', *options, '--target', target, status[0]['latest'])
            if (target / 'file.txt').read_text(encoding='utf-8') != 'native scheduled recovery':
                raise RuntimeError('native scheduled backup recovered incorrect contents')
        finally:
            if installed:
                # Stop only our owned task if a failure interrupted the test.
                # Removing the native definition also prevents future runs.
                state = schedule('status')['state']
                if state != 'missing':
                    if system == 'Windows':
                        windows_action('stop')
                    elif system == 'Linux':
                        subprocess.run(['systemctl', '--user', 'stop', f'veyra-{name}.service'], check=True, capture_output=True)
                    schedule('remove')
                if schedule('status')['state'] != 'missing':
                    raise RuntimeError('disposable native schedule was not removed')
        print(f'::notice::Native {system} schedule preview/install/status/disable/enable/remove, successful backup, unavailable-drive failure, retry and recovery passed')


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        message = str(error)
        if isinstance(error, subprocess.CalledProcessError):
            message += '\n' + str(error.stdout or '') + '\n' + str(error.stderr or '')
        detail = message.replace('%', '%25').replace('\r', '%0D').replace('\n', '%0A')
        print('::error::Native scheduled backup failed: ' + detail, flush=True)
        raise
