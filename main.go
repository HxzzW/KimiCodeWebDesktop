package main

// Kimi Web 桌面启动器(Go 版)。
//
// 启动 `kimi web --no-open` 作为隐藏子进程(或复用已在运行的实例),
// 用内嵌 WebView2 窗口打开 Web UI;关闭窗口只是隐藏到系统托盘。
// 托盘菜单:显示窗口 / 会话可视化 / 轮换 Token / 检查更新 / 重启服务 / 通知设置 / 退出。

import "fmt"

func main() {
	// 无控制台窗口,异常不能静默吞掉(例如 WebView2 数据目录被
	// 未走互斥锁的进程占用导致初始化失败),弹窗告知
	defer func() {
		if r := recover(); r != nil {
			messageBox(fmt.Sprintf("发生未处理的错误:\n\n%v", r), mbIconWarning)
		}
	}()

	setProcessDPIAware()
	if !ensureSingleInstance() {
		return
	}

	kimi := findKimi()
	if kimi == "" {
		showError("未找到 kimi 命令,请先安装 Kimi Code CLI。")
		return
	}

	if !webview2RuntimeAvailable() {
		messageBox(
			"未检测到 WebView2 运行时,本程序无法运行。\n\n"+
				"请从微软官网下载安装 WebView2 Runtime:\n"+
				"https://developer.microsoft.com/microsoft-edge/webview2/",
			mbIconWarning,
		)
		return
	}

	checkForUpdates(kimi, false, nil) // 服务尚未启动,升级后自然用新版本

	a := newApp(kimi)
	if a.startService() == 0 {
		runDoctor(kimi)
		showError("kimi web 服务启动失败或超时。\n日志见: " + serverLog)
		return
	}

	a.run()
}
