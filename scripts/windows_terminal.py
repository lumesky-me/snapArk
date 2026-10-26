"""Minimal native ConPTY driver for packaged Windows terminal regression tests."""
import ctypes
from ctypes import wintypes as w
import os
import queue
import re
import subprocess
import threading
import time


class StartupInfo(ctypes.Structure):
    _fields_ = [('cb', w.DWORD), ('lpReserved', w.LPWSTR), ('lpDesktop', w.LPWSTR),
                ('lpTitle', w.LPWSTR), ('dwX', w.DWORD), ('dwY', w.DWORD),
                ('dwXSize', w.DWORD), ('dwYSize', w.DWORD), ('dwXCountChars', w.DWORD),
                ('dwYCountChars', w.DWORD), ('dwFillAttribute', w.DWORD), ('dwFlags', w.DWORD),
                ('wShowWindow', w.WORD), ('cbReserved2', w.WORD), ('lpReserved2', ctypes.c_void_p),
                ('hStdInput', w.HANDLE), ('hStdOutput', w.HANDLE), ('hStdError', w.HANDLE)]


class StartupInfoEx(ctypes.Structure):
    _fields_ = [('StartupInfo', StartupInfo), ('lpAttributeList', ctypes.c_void_p)]


class ProcessInfo(ctypes.Structure):
    _fields_ = [('hProcess', w.HANDLE), ('hThread', w.HANDLE), ('dwProcessId', w.DWORD), ('dwThreadId', w.DWORD)]


def drive(binary, args, answers, secret, expected_exit=0):
    kernel = ctypes.WinDLL('kernel32', use_last_error=True)
    kernel.CreatePipe.argtypes = [ctypes.POINTER(w.HANDLE), ctypes.POINTER(w.HANDLE), ctypes.c_void_p, w.DWORD]
    kernel.CreatePipe.restype = w.BOOL
    kernel.CreatePseudoConsole.argtypes = [w.DWORD, w.HANDLE, w.HANDLE, w.DWORD, ctypes.POINTER(w.HANDLE)]
    kernel.CreatePseudoConsole.restype = ctypes.c_long
    kernel.ClosePseudoConsole.argtypes = [w.HANDLE]
    kernel.ClosePseudoConsole.restype = None
    kernel.InitializeProcThreadAttributeList.argtypes = [ctypes.c_void_p, w.DWORD, w.DWORD, ctypes.POINTER(ctypes.c_size_t)]
    kernel.UpdateProcThreadAttribute.argtypes = [ctypes.c_void_p, w.DWORD, ctypes.c_size_t, ctypes.c_void_p, ctypes.c_size_t, ctypes.c_void_p, ctypes.c_void_p]
    kernel.DeleteProcThreadAttributeList.argtypes = [ctypes.c_void_p]
    kernel.CreateProcessW.argtypes = [w.LPCWSTR, w.LPWSTR, ctypes.c_void_p, ctypes.c_void_p, w.BOOL,
                                     w.DWORD, ctypes.c_void_p, w.LPCWSTR, ctypes.POINTER(StartupInfoEx), ctypes.POINTER(ProcessInfo)]
    kernel.CreateProcessW.restype = w.BOOL
    kernel.CloseHandle.argtypes = [w.HANDLE]
    kernel.WaitForSingleObject.argtypes = [w.HANDLE, w.DWORD]
    kernel.GetExitCodeProcess.argtypes = [w.HANDLE, ctypes.POINTER(w.DWORD)]
    kernel.TerminateProcess.argtypes = [w.HANDLE, w.UINT]

    def checked(result):
        if not result:
            raise ctypes.WinError(ctypes.get_last_error())

    input_read, input_write, output_read, output_write = w.HANDLE(), w.HANDLE(), w.HANDLE(), w.HANDLE()
    console = w.HANDLE()
    proc = ProcessInfo()
    attributes = None
    reader = None
    input_fd = output_fd = None
    blocks = queue.Queue()
    transcript, pending = bytearray(), bytearray()
    index = 0
    try:
        checked(kernel.CreatePipe(ctypes.byref(input_read), ctypes.byref(input_write), None, 0))
        checked(kernel.CreatePipe(ctypes.byref(output_read), ctypes.byref(output_write), None, 0))
        # COORD is two signed 16-bit integers passed by value (packed in DWORD).
        result = kernel.CreatePseudoConsole(500 | (60 << 16), input_read, output_write, 0, ctypes.byref(console))
        if result != 0:
            raise OSError(f'CreatePseudoConsole HRESULT={result:#x}')
        size = ctypes.c_size_t()
        kernel.InitializeProcThreadAttributeList(None, 1, 0, ctypes.byref(size))
        attributes = ctypes.create_string_buffer(size.value)
        checked(kernel.InitializeProcThreadAttributeList(attributes, 1, 0, ctypes.byref(size)))
        # PSEUDOCONSOLE expects the handle value, not a pointer to the handle.
        checked(kernel.UpdateProcThreadAttribute(attributes, 0, 0x00020016, console, ctypes.sizeof(w.HANDLE), None, None))
        startup = StartupInfoEx()
        startup.StartupInfo.cb = ctypes.sizeof(startup)
        startup.lpAttributeList = ctypes.addressof(attributes)
        command = ctypes.create_unicode_buffer(subprocess.list2cmdline([str(binary), *map(str, args)]))
        checked(kernel.CreateProcessW(str(binary), command, None, None, False, 0x00080000,
                                      None, None, ctypes.byref(startup), ctypes.byref(proc)))
        kernel.CloseHandle(proc.hThread)
        proc.hThread = None
        for handle in (input_read, output_write):
            kernel.CloseHandle(handle)
            handle.value = None
        import msvcrt
        input_fd = msvcrt.open_osfhandle(input_write.value, os.O_WRONLY | os.O_BINARY)
        input_write.value = None
        output_fd = msvcrt.open_osfhandle(output_read.value, os.O_RDONLY | os.O_BINARY)
        output_read.value = None

        def read_output():
            try:
                while True:
                    block = os.read(output_fd, 65536)
                    if not block:
                        break
                    blocks.put(block)
            except OSError as error:
                # CRT pipe reads can surface ERROR_BROKEN_PIPE as EINVAL.
                if error.errno not in (22, 32):
                    blocks.put(str(error).encode('utf-8'))
            finally:
                blocks.put(None)

        reader = threading.Thread(target=read_output, daemon=True)
        reader.start()
        deadline = time.monotonic() + 120
        while time.monotonic() < deadline:
            try:
                block = blocks.get(timeout=0.1)
            except queue.Empty:
                block = b''
            if block:
                transcript.extend(block)
                pending.extend(block)
                if index < len(answers):
                    prompt, reply = answers[index]
                    if prompt.encode('utf-8') in pending:
                        time.sleep(0.05)
                        os.write(input_fd, reply.encode('utf-8') + b'\r')
                        pending.clear()
                        index += 1
            if kernel.WaitForSingleObject(proc.hProcess, 0) == 0:
                break
        else:
            raise RuntimeError('Windows terminal workflow timed out: ' + transcript.decode('utf-8', errors='replace'))
        exit_code = w.DWORD()
        checked(kernel.GetExitCodeProcess(proc.hProcess, ctypes.byref(exit_code)))
        # The reader remains active while ClosePseudoConsole flushes output.
        kernel.ClosePseudoConsole(console)
        console.value = None
        reader.join(timeout=10)
        if reader.is_alive():
            raise RuntimeError('Windows terminal output did not close')
        while not blocks.empty():
            block = blocks.get_nowait()
            if block:
                transcript.extend(block)
        output = transcript.decode('utf-8', errors='replace')
        output = re.sub(r'\x1b\][^\x07]*(?:\x07|\x1b\\)', '', output)
        output = re.sub(r'\x1b\[[0-?]*[ -/]*[@-~]', '', output)
        if secret in output:
            raise RuntimeError('hidden password appeared in Windows terminal output')
        if exit_code.value != expected_exit or index != len(answers):
            raise RuntimeError(f'Windows terminal workflow failed (exit {exit_code.value}): {output}')
        return output
    finally:
        if proc.hProcess:
            if kernel.WaitForSingleObject(proc.hProcess, 0) != 0:
                kernel.TerminateProcess(proc.hProcess, 1)
                kernel.WaitForSingleObject(proc.hProcess, 5000)
            kernel.CloseHandle(proc.hProcess)
        if console.value:
            kernel.ClosePseudoConsole(console)
        if reader:
            reader.join(timeout=5)
        for fd in (input_fd, output_fd):
            if fd is not None:
                os.close(fd)
        for handle in (input_read, input_write, output_read, output_write):
            if handle.value:
                kernel.CloseHandle(handle)
        if attributes is not None:
            kernel.DeleteProcThreadAttributeList(attributes)
