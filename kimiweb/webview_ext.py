# -*- coding: utf-8 -*-
"""pywebview 的补丁与统一启动参数。"""

import webview

from .config import APP_TITLE, WEBVIEW_DATA_DIR


def patch_webview_settings():
    """pywebview 只在 debug 模式下开启右键菜单和 Ctrl+C/V 等快捷键,
    这里打补丁强制开启(仍不开启 DevTools)。"""
    try:
        from webview.platforms import edgechromium
    except Exception:
        return
    original = edgechromium.EdgeChrome.on_webview_ready

    def patched(self, sender, args):
        original(self, sender, args)
        try:
            if args.IsSuccess:
                settings = sender.CoreWebView2.Settings
                settings.AreDefaultContextMenusEnabled = True
                settings.AreBrowserAcceleratorKeysEnabled = True
        except Exception:
            pass

    edgechromium.EdgeChrome.on_webview_ready = patched


def start_webview():
    """统一启动参数:关闭隐私模式并指定固定数据目录,
    localStorage/cookies 才能跨启动保留。"""
    webview.start(private_mode=False, storage_path=WEBVIEW_DATA_DIR)


def show_error(message):
    webview.create_window(
        APP_TITLE,
        html=f"<h3 style='font-family:sans-serif'>{message.replace(chr(10), '<br>')}</h3>",
        width=640,
        height=300,
        text_select=True,
    )
    start_webview()
