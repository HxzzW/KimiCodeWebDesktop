# -*- coding: utf-8 -*-
"""程序入口:根目录 app.py 与 `python -m kimiweb` 都走这里。"""

import sys

from .app import KimiWebApp
from .config import MB_ICONWARNING, SERVER_LOG
from .server import run_doctor
from .updater import check_for_updates
from .utils import find_kimi
from .webview_ext import patch_webview_settings, show_error
from .winapi import ensure_single_instance, message_box, webview2_runtime_available


def main():
    patch_webview_settings()
    if not ensure_single_instance():
        return 0

    kimi = find_kimi()
    if not kimi:
        show_error("未找到 kimi 命令,请先安装 Kimi Code CLI。")
        return 1

    if not webview2_runtime_available():
        message_box(
            "未检测到 WebView2 运行时,本程序无法运行。\n\n"
            "请从微软官网下载安装 WebView2 Runtime:\n"
            "https://developer.microsoft.com/microsoft-edge/webview2/",
            MB_ICONWARNING,
        )
        return 1

    check_for_updates(kimi)

    app = KimiWebApp(kimi)
    if app.start_service() is None:
        run_doctor(kimi)
        show_error(f"kimi web 服务启动失败或超时。\n日志见: {SERVER_LOG}")
        return 1

    return app.run()


def entry():
    try:
        sys.exit(main())
    except Exception:
        # 无控制台窗口,异常不能静默吞掉,弹窗告知
        import traceback

        message_box(f"发生未处理的错误:\n\n{traceback.format_exc(limit=3)}", MB_ICONWARNING)
        sys.exit(1)


if __name__ == "__main__":
    entry()
