package main

import (
	"github.com/getlantern/systray"
)

var trayIconCh = make(chan []byte, 4)

// setTrayIcon 从任意 goroutine 换托盘图标(转发到托盘线程执行)
func setTrayIcon(ico []byte) {
	select {
	case trayIconCh <- ico:
	default:
	}
}

func stopTray() {
	systray.Quit()
}

func (a *app) setupTray() {
	a.baseIconICO = kimiIcoBytes
	a.spinnerFrames = makeSpinnerFrames()
	go systray.Run(func() {
		systray.SetIcon(kimiIcoBytes)
		systray.SetTooltip(appTitle)

		mShow := systray.AddMenuItem("显示窗口", "显示主窗口")
		mVis := systray.AddMenuItem("会话可视化", "启动/停止 kimi vis")
		mRotate := systray.AddMenuItem("轮换 Token", "轮换 web UI 的 bearer token")
		mUpdate := systray.AddMenuItem("检查更新", "检测 Kimi Code CLI 新版本")
		mRestart := systray.AddMenuItem("重启服务", "重启 kimi web 服务")
		mSettings := systray.AddMenuItem("通知设置", "动画/通知/提示音开关")
		cAnim := mSettings.AddSubMenuItemCheckbox("工作时动画", "", getSetting("anim_busy"))
		cToast := mSettings.AddSubMenuItemCheckbox("完成时通知", "", getSetting("notify_toast"))
		cSound := mSettings.AddSubMenuItemCheckbox("完成时提示音", "", getSetting("notify_sound"))
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("退出", "停止服务并退出")

		for {
			select {
			case <-mShow.ClickedCh:
				a.showMainWindow()
			case <-mVis.ClickedCh:
				go a.toggleVis()
			case <-mRotate.ClickedCh:
				go a.rotateToken()
			case <-mUpdate.ClickedCh:
				go checkForUpdates(a.kimi, true)
			case <-mRestart.ClickedCh:
				go a.restartService()
			case <-cAnim.ClickedCh:
				v := !getSetting("anim_busy")
				setSetting("anim_busy", v)
				if v {
					cAnim.Check()
				} else {
					cAnim.Uncheck()
				}
			case <-cToast.ClickedCh:
				v := !getSetting("notify_toast")
				setSetting("notify_toast", v)
				if v {
					cToast.Check()
				} else {
					cToast.Uncheck()
				}
			case <-cSound.ClickedCh:
				v := !getSetting("notify_sound")
				setSetting("notify_sound", v)
				if v {
					cSound.Check()
				} else {
					cSound.Uncheck()
				}
			case ico := <-trayIconCh:
				systray.SetIcon(ico)
			case <-mQuit.ClickedCh:
				a.quit()
				return
			}
		}
	}, func() {})
}
