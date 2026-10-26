"""Shared helpers for disposable packaged workflow fixtures."""
import json
import platform
import subprocess


def protect_windows_file(path):
    if platform.system() != 'Windows':
        return
    # Restrict only this newly created disposable test credential. Data is sent
    # over stdin, never interpolated into PowerShell code or command arguments.
    script = r'''$ErrorActionPreference='Stop'
$r=[Console]::In.ReadToEnd()|ConvertFrom-Json
$sid=[System.Security.Principal.WindowsIdentity]::GetCurrent().User
$acl=New-Object System.Security.AccessControl.FileSecurity
$acl.SetOwner($sid)
$acl.SetAccessRuleProtection($true,$false)
$acl.AddAccessRule([System.Security.AccessControl.FileSystemAccessRule]::new($sid,'FullControl','Allow'))
[System.IO.File]::SetAccessControl([string]$r.path,$acl)
'''
    subprocess.run(['powershell.exe', '-NoProfile', '-NonInteractive', '-Command', script],
                   input=json.dumps({'path': str(path)}), text=True, check=True,
                   capture_output=True, timeout=30)
