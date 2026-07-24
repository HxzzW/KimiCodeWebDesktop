# -*- coding: utf-8 -*-
"""kimi web 本地服务的发现、拉起与就绪等待。"""

import json
import os
import socket
import subprocess
import time
import urllib.error
import urllib.request

from .config import (
    CREATE_NO_WINDOW,
    PREFERRED_PORT,
    PORT_SCAN_LIMIT,
    SERVER_LOG,
    STARTUP_TIMEOUT,
    WEBVIEW_DATA_DIR,
)
from .utils import read_token
from .winapi import pid_alive


def probe_existing_server(port):
    """探测指定端口是否是 kimi web:用 /openapi.json 做指纹识别,
    避免误连占用同一端口的无关服务。"""
    req = urllib.request.Request(f"http://127.0.0.1:{port}/openapi.json")
    token = read_token()
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    try:
        with urllib.request.urlopen(req, timeout=2) as resp:
            return b'"openapi"' in resp.read(4096)
    except Exception:
        return False


def find_running_server():
    """从 kimi 的实例注册表找一个活着的 kimi web 实例(校验 pid 存活 +
    openapi 指纹),返回其端口;没有则返回 None。"""
    instances_dir = os.path.expanduser(r"~\.kimi-code\server\instances")
    try:
        files = sorted(
            (
                os.path.join(instances_dir, f)
                for f in os.listdir(instances_dir)
                if f.endswith(".json")
            ),
            key=os.path.getmtime,
            reverse=True,
        )
    except OSError:
        return None
    for path in files:
        try:
            with open(path, "r", encoding="utf-8") as f:
                info = json.load(f)
        except (OSError, ValueError):
            continue
        pid, host, port = info.get("pid"), info.get("host"), info.get("port")
        if not isinstance(pid, int) or not isinstance(port, int):
            continue
        if host != "127.0.0.1":
            continue
        if pid_alive(pid) and probe_existing_server(port):
            return port
    return None


def pick_port():
    """优先使用固定端口(保持 localStorage 有效);被占用则顺延,
    都不空闲再交给系统随机分配。"""
    for port in range(PREFERRED_PORT, PREFERRED_PORT + PORT_SCAN_LIMIT):
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
            try:
                s.bind(("127.0.0.1", port))
                return port
            except OSError:
                continue
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def wait_for_server(port, proc, timeout=STARTUP_TIMEOUT):
    """轮询直到服务就绪(用 openapi 指纹确认,避免误判无关服务)。
    kimi web 在端口被占时会自动 +1,所以探测一小段连续端口。
    返回实际端口,失败返回 None。"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        if proc.poll() is not None:
            return None  # 进程已退出
        for p in range(port, port + 5):
            if probe_existing_server(p):
                return p
        time.sleep(0.3)
    return None


def run_doctor(kimi):
    """服务启动失败时运行 kimi doctor,把配置校验结果附加到日志。"""
    try:
        result = subprocess.run(
            [kimi, "doctor"],
            capture_output=True,
            text=True,
            timeout=20,
            creationflags=CREATE_NO_WINDOW,
        )
        output = (result.stdout + "\n" + result.stderr).strip()
    except (OSError, subprocess.SubprocessError):
        return
    if output:
        try:
            os.makedirs(WEBVIEW_DATA_DIR, exist_ok=True)
            with open(SERVER_LOG, "a", encoding="utf-8") as f:
                f.write("\n--- kimi doctor ---\n" + output + "\n")
        except OSError:
            pass
