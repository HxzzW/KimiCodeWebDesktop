# -*- coding: utf-8 -*-
"""Kimi Code CLI 版本检测与升级。"""

import json
import re
import subprocess
import threading
import time
import urllib.request

from .config import (
    CHECK_INTERVAL,
    CREATE_NO_WINDOW,
    IDCANCEL,
    IDYES,
    MB_ICONINFORMATION,
    MB_ICONQUESTION,
    MB_ICONWARNING,
    MB_YESNOCANCEL,
    NPM_LATEST_URLS,
    UPGRADE_COMMAND,
    load_state,
    save_state,
)
from .winapi import message_box


def parse_version(text):
    """从字符串里提取 (major, minor, patch) 数字版本号。"""
    nums = re.findall(r"\d+", text)
    return tuple(int(n) for n in nums[:3])


def _norm(version):
    return (tuple(version) + (0, 0, 0))[:3]


def format_version(version):
    return ".".join(str(n) for n in _norm(version))


def current_version(kimi):
    """运行 kimi --version 取当前版本;失败返回空元组。"""
    try:
        result = subprocess.run(
            [kimi, "--version"],
            capture_output=True,
            text=True,
            timeout=15,
            creationflags=CREATE_NO_WINDOW,
        )
        return parse_version(result.stdout or result.stderr)
    except (OSError, subprocess.SubprocessError):
        return ()


def latest_version():
    """查询最新版本(npm registry,失败走国内镜像);网络失败返回空元组。"""
    for url in NPM_LATEST_URLS:
        try:
            with urllib.request.urlopen(url, timeout=6) as resp:
                data = json.loads(resp.read().decode("utf-8"))
            version = parse_version(data.get("version", ""))
            if version:
                return version
        except Exception:
            continue
    return ()


_check_lock = threading.Lock()


def check_for_updates(kimi, force=False):
    """可重入保护:上一次检测/弹窗没结束时,忽略重复触发(网络查询
    要数秒,用户连点托盘菜单会导致弹窗排队,看起来像关不掉)。"""
    if not _check_lock.acquire(blocking=False):
        return
    try:
        _check_for_updates(kimi, force)
    finally:
        _check_lock.release()


def _check_for_updates(kimi, force):
    """检测 Kimi Code CLI 更新(每 24h 最多一次,离线也计入,避免每次启动
    都卡在超时上;force 跳过间隔与跳过记录,并给出结果反馈)。
    有新版本时询问:升级 / 本次跳过 / 跳过此版本。"""
    state = load_state()
    if not force and time.time() - state.get("last_check", 0) < CHECK_INTERVAL:
        return
    new = latest_version()
    state["last_check"] = time.time()
    save_state(state)
    if not new:
        if force:
            message_box("暂时无法检查更新:网络不可用。", MB_ICONWARNING)
        return
    cur = current_version(kimi)
    if not cur or _norm(new) <= _norm(cur):
        if force:
            message_box(f"已是最新版本 v{format_version(cur or new)}。", MB_ICONINFORMATION)
        return
    if not force and format_version(new) == state.get("skip_version"):
        return
    answer = message_box(
        f"检测到 Kimi Code CLI 新版本 v{format_version(new)}"
        f"(当前 v{format_version(cur)})。\n\n"
        "是:立即升级\n否:本次跳过\n取消:跳过此版本,不再提示",
        MB_YESNOCANCEL | MB_ICONQUESTION,
    )
    if answer == IDCANCEL:
        state["skip_version"] = format_version(new)
        save_state(state)
        return
    if answer != IDYES:
        return
    # 官方 Windows 安装/升级脚本;开可见控制台窗口显示下载进度
    code = subprocess.call(
        ["powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", UPGRADE_COMMAND]
    )
    if code == 0:
        state.pop("skip_version", None)
        save_state(state)
        message_box("升级完成,将以新版本启动。", MB_ICONINFORMATION)
    else:
        message_box("升级未完成(可能被取消或网络失败),将继续使用当前版本。", MB_ICONWARNING)
