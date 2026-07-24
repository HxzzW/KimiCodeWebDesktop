# -*- coding: utf-8 -*-
"""Windows 专有原语:消息框、进程存活判断、单实例锁、WebView2 运行时检测。

命名为 winapi 而非 win32,避免遮蔽 pywin32 包。
"""

import ctypes
import winreg
from ctypes import wintypes

from .config import (
    APP_TITLE,
    ERROR_ALREADY_EXISTS,
    MB_ICONINFORMATION,
    MB_SETFOREGROUND,
    MB_TOPMOST,
    PROCESS_QUERY_LIMITED_INFORMATION,
    STILL_ACTIVE,
    WEBVIEW2_GUID,
)


def message_box(text, flags):
    # 统一置顶并抢占前台:无控制台程序里弹窗容易被主窗口挡住
    return ctypes.windll.user32.MessageBoxW(
        0, text, APP_TITLE, flags | MB_SETFOREGROUND | MB_TOPMOST
    )


def pid_alive(pid):
    """判断进程是否存活(注意:Windows 上不能用 os.kill(pid, 0),那会杀进程)。"""
    handle = ctypes.windll.kernel32.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, False, pid)
    if not handle:
        return False
    try:
        code = wintypes.DWORD()
        if not ctypes.windll.kernel32.GetExitCodeProcess(handle, ctypes.byref(code)):
            return False
        return code.value == STILL_ACTIVE
    finally:
        ctypes.windll.kernel32.CloseHandle(handle)


def webview2_runtime_available():
    """通过注册表检测 WebView2 Runtime 是否安装。"""
    for root in (winreg.HKEY_LOCAL_MACHINE, winreg.HKEY_CURRENT_USER):
        for scope in (r"SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients",
                      r"SOFTWARE\Microsoft\EdgeUpdate\Clients"):
            try:
                with winreg.OpenKey(root, scope + "\\" + WEBVIEW2_GUID) as key:
                    pv, _ = winreg.QueryValueEx(key, "pv")
                    if pv and pv != "0.0.0.0":
                        return True
            except OSError:
                continue
    return False


def ensure_single_instance():
    """单实例保护:已在运行时提示并退出,避免重复窗口和重复更新弹窗。"""
    ctypes.windll.kernel32.CreateMutexW(None, False, "KimiWebSingleton")
    if ctypes.windll.kernel32.GetLastError() == ERROR_ALREADY_EXISTS:
        message_box("Kimi Web 已在运行,请使用已打开的窗口或托盘图标。", MB_ICONINFORMATION)
        return False
    return True
