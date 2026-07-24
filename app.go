package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	webview2 "github.com/jchv/go-webview2"
)

var quitting atomic.Int32

// spinnerChars 与浏览器端页面标题的转圈字符一致
var spinnerChars = []string{"◐", "◓", "◑", "◒"}

const animInterval = 500 * time.Millisecond // 标题动画帧间隔;托盘按其 2 倍间隔,避免闪烁感

type app struct {
	kimi     string
	port     int
	cmd      *exec.Cmd // 本进程拉起的 kimi web;复用别人实例时为 nil
	procDone chan struct{}
	logFile  *os.File
	visCmd   *exec.Cmd

	w         webview2.WebView
	hwnd      uintptr
	animating atomic.Int32

	baseIconICO   []byte
	spinnerFrames [][]byte
	lastRectSave  time.Time
}

func newApp(kimi string) *app {
	return &app{kimi: kimi}
}

func (a *app) buildURL() string {
	url := fmt.Sprintf("http://127.0.0.1:%d/", a.port)
	if t := readToken(); t != "" {
		url += "#token=" + t
	}
	return url
}

// startService 复用或拉起 kimi web;成功返回端口,失败返回 0
func (a *app) startService() int {
	if port := findRunningServer(); port != 0 {
		a.port = port
		return port
	}
	port := pickPort()
	cmd, err := spawnService(a.kimi, port, &a.logFile)
	if err != nil {
		return 0
	}
	a.cmd = cmd
	a.procDone = make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(a.procDone)
	}()
	ready := waitForServer(port, cmd)
	if ready == 0 {
		_ = cmd.Process.Kill()
		return 0
	}
	a.port = ready
	return ready
}

func (a *app) serviceDead() bool {
	if a.cmd == nil || a.procDone == nil {
		return false
	}
	select {
	case <-a.procDone:
		return true
	default:
		return false
	}
}

func (a *app) stopService() {
	// 只停止本进程拉起的服务;复用的实例不动
	if a.cmd != nil && a.cmd.Process != nil && !a.serviceDead() {
		_ = a.cmd.Process.Kill()
		<-a.procDone // 等 waiter goroutine 回收
	}
}

func (a *app) restartService() {
	a.stopService()
	if a.startService() == 0 {
		runDoctor(a.kimi)
		messageBox("服务重启失败。\n日志见: "+serverLog, mbIconWarning)
		return
	}
	url := a.buildURL()
	a.w.Dispatch(func() { a.w.Navigate(url) })
}

// watchdog 监视本进程拉起的服务,意外退出时询问是否重启
func (a *app) watchdog() {
	for quitting.Load() == 0 {
		time.Sleep(2 * time.Second)
		if !a.serviceDead() {
			continue
		}
		if quitting.Load() != 0 {
			return
		}
		if messageBox("kimi web 服务意外退出。\n\n是否重启服务?", mbYesNo|mbIconWarning) == idYes {
			a.restartService()
		} else {
			return
		}
	}
}

// activityMonitor 轮询会话工作状态:忙时开启动画,单个会话完成时带标题通知,
// 有会话等待交互时提醒;完成与等待时闪烁任务栏
func (a *app) activityMonitor() {
	wasBusy := false
	busySessions := map[string]string{} // 在忙的会话 id -> 标题
	pendingSessions := map[string]bool{}
	for quitting.Load() == 0 {
		time.Sleep(2 * time.Second)
		list, ok := fetchActivities(a.port)
		if !ok {
			continue
		}
		nowBusy := map[string]string{}
		present := map[string]bool{}
		anyBusy := false
		for _, s := range list {
			present[s.ID] = true
			if s.Busy {
				nowBusy[s.ID] = s.Title
				anyBusy = true
			}
			if s.Pending && !pendingSessions[s.ID] {
				pendingSessions[s.ID] = true
				if getSetting("notify_toast") {
					toastNotify(fmt.Sprintf("「%s」等待你的操作(审批或提问)", shortTitle(s.Title)))
					flashWindow(a.hwnd, 5)
				}
			} else if !s.Pending {
				delete(pendingSessions, s.ID)
			}
		}
		// 之前在忙、现在不忙的会话即完成;整个消失的视为删除,静默略过
		var finished []string
		for id, title := range busySessions {
			if _, still := nowBusy[id]; !still && present[id] {
				finished = append(finished, title)
			}
		}
		busySessions = nowBusy
		if anyBusy != wasBusy {
			wasBusy = anyBusy
			if anyBusy {
				a.startAnimation()
			} else {
				a.stopAnimation()
			}
		}
		if len(finished) > 0 {
			a.onSessionsFinished(finished)
		}
	}
}

func (a *app) onSessionsFinished(titles []string) {
	if getSetting("notify_toast") {
		if len(titles) > 3 {
			toastNotify(fmt.Sprintf("%d 个任务已完成", len(titles)))
		} else {
			for _, t := range titles {
				toastNotify(fmt.Sprintf("「%s」已完成", shortTitle(t)))
			}
		}
		flashWindow(a.hwnd, 5)
	}
	if getSetting("notify_sound") {
		playCompletionSound()
	}
}

// shortTitle 截断过长的会话标题用于通知
func shortTitle(t string) string {
	t = strings.TrimSpace(t)
	if t == "" {
		return "未命名会话"
	}
	r := []rune(t)
	if len(r) > 20 {
		return string(r[:20]) + "…"
	}
	return t
}

func (a *app) startAnimation() {
	if getSetting("anim_busy") {
		a.animating.Store(1)
	}
}

func (a *app) stopAnimation() {
	a.animating.Store(0)
}

// animateLoop 常驻动画线程:animating 置位期间驱动标题与托盘帧,清除后恢复静态
func (a *app) animateLoop() {
	i := 0
	wasActive := false
	for quitting.Load() == 0 {
		if a.animating.Load() != 0 {
			frame := spinnerChars[i%len(spinnerChars)]
			a.w.Dispatch(func() { a.w.SetTitle(fmt.Sprintf("%s %s 工作中", appTitle, frame)) })
			if a.spinnerFrames != nil && i%2 == 0 {
				setTrayIcon(a.spinnerFrames[(i/2)%len(a.spinnerFrames)])
			}
			i++
			wasActive = true
			time.Sleep(animInterval)
		} else {
			if wasActive {
				wasActive = false
				i = 0
				a.w.Dispatch(func() { a.w.SetTitle(appTitle) })
				if a.baseIconICO != nil {
					setTrayIcon(a.baseIconICO)
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
}

func (a *app) showMainWindow() {
	showWindow(a.hwnd, swRestore)
	setForeground(a.hwnd)
}

// ---- 窗口位置记忆 ----

// saveWindowRect 拖动/缩放/最大化及正常退出时保存窗口几何。
// 用 GetWindowPlacement:最大化/最小化时存的也是正确的还原矩形
func (a *app) saveWindowRect() {
	if time.Since(a.lastRectSave) < time.Second {
		return // WM_SIZE 在拖动期间连续触发,防抖
	}
	a.lastRectSave = time.Now()
	showCmd, x, y, w, h, ok := getWindowPlacement(a.hwnd)
	if !ok {
		return
	}
	s := loadState()
	s["win_x"], s["win_y"], s["win_w"], s["win_h"] =
		float64(x), float64(y), float64(w), float64(h)
	s["win_max"] = showCmd == swMaximize
	saveState(s)
}

// loadWindowRect 读取保存的窗口几何;掉出虚拟屏(如显示器变更)则放弃恢复
func loadWindowRect() (x, y, w, h int, maxed, ok bool) {
	s := loadState()
	fx, xok := s["win_x"].(float64)
	fy, yok := s["win_y"].(float64)
	fw, wok := s["win_w"].(float64)
	fh, hok := s["win_h"].(float64)
	if !(xok && yok && wok && hok) || fw < 200 || fh < 200 {
		return 0, 0, 0, 0, false, false
	}
	x, y, w, h = int(fx), int(fy), int(fw), int(fh)
	vx, vy, vw, vh := virtualScreenBounds()
	if x+w < vx || y+h < vy || x > vx+vw || y > vy+vh {
		return 0, 0, 0, 0, false, false
	}
	maxed, _ = s["win_max"].(bool)
	return x, y, w, h, maxed, true
}

// toggleVis 启动/停止 kimi vis 会话可视化器
func (a *app) toggleVis() {
	if a.visCmd != nil && a.visCmd.Process != nil && pidAlive(a.visCmd.Process.Pid) {
		_ = a.visCmd.Process.Kill()
		toastNotify("会话可视化已停止")
		return
	}
	cmd := exec.Command(a.kimi, "vis")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	if err := cmd.Start(); err != nil {
		messageBox("kimi vis 启动失败: "+err.Error(), mbIconWarning)
		return
	}
	a.visCmd = cmd
	toastNotify("会话可视化已启动,浏览器将自动打开")
}

// rotateToken 轮换 web UI 的 bearer token,并用新 token 重载页面
func (a *app) rotateToken() {
	cmd := exec.Command(a.kimi, "web", "rotate-token")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	if err := cmd.Run(); err != nil {
		messageBox("Token 轮换失败: "+err.Error(), mbIconWarning)
		return
	}
	url := a.buildURL()
	a.w.Dispatch(func() { a.w.Navigate(url) })
	toastNotify("Token 已轮换,页面已用新令牌重新加载")
}

func (a *app) run() int {
	a.w = webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:  webviewDataDir,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  appTitle,
			Width:  1280,
			Height: 840,
			Center: true,
			IconId: 1, // exe 内嵌图标资源(goversioninfo 生成)
		},
	})
	if a.w == nil {
		messageBox("WebView2 初始化失败。", mbIconWarning)
		return 1
	}
	defer a.w.Destroy()
	a.hwnd = uintptr(a.w.Window())
	subclassForCloseHide(a.hwnd, &quitting, a.saveWindowRect)
	if x, y, w, h, maxed, ok := loadWindowRect(); ok {
		setWindowRect(a.hwnd, x, y, w, h) // 恢复上次的窗口位置与大小
		if maxed {
			showWindow(a.hwnd, swMaximize)
		}
	}
	a.w.Navigate(a.buildURL())

	a.setupTray()
	go a.watchdog()
	go a.activityMonitor()
	go a.animateLoop()

	a.w.Run() // 阻塞,直到窗口真正关闭

	// 清理
	a.lastRectSave = time.Time{} // 退出保存绕过防抖
	a.saveWindowRect()
	a.stopService()
	if a.visCmd != nil && a.visCmd.Process != nil && pidAlive(a.visCmd.Process.Pid) {
		_ = a.visCmd.Process.Kill()
	}
	if a.logFile != nil {
		_ = a.logFile.Close()
	}
	stopTray()
	return 0
}

func (a *app) quit() {
	quitting.Store(1)
	postClose(a.hwnd) // 触发真正关窗(子类化过程放行),随后 run() 清理
}

// showError 用小窗口展示错误信息
func showError(message string) {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath: webviewDataDir,
		WindowOptions: webview2.WindowOptions{
			Title:  appTitle,
			Width:  640,
			Height: 300,
			Center: true,
		},
	})
	if w == nil {
		return
	}
	defer w.Destroy()
	html := "<h3 style='font-family:sans-serif'>" + message + "</h3>"
	w.SetHtml(html)
	w.Run()
}
