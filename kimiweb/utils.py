# -*- coding: utf-8 -*-
"""通用小工具:定位 kimi、读取 web UI token、打包资源路径。"""

import os
import shutil
import sys


def find_kimi():
    """定位 kimi 可执行文件:优先 PATH,其次默认安装位置。"""
    path = shutil.which("kimi")
    if path:
        return path
    default = os.path.expanduser(r"~\.kimi-code\bin\kimi.exe")
    if os.path.isfile(default):
        return default
    return None


def read_token():
    token_file = os.path.expanduser(r"~\.kimi-code\server.token")
    try:
        with open(token_file, "r", encoding="utf-8") as f:
            return f.read().strip()
    except OSError:
        return ""


def resource_path(name):
    """兼容 PyInstaller 单文件运行时的资源定位。
    开发模式下以项目根目录(包的上一级)为基准。"""
    base = getattr(
        sys, "_MEIPASS", os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    )
    return os.path.join(base, name)
