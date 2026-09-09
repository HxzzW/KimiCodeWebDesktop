package main

// 远程操控(Kimi Remote Control,CLI ≥0.42 的实验功能):
// kimi web 加 --rc 并设 KIMI_CODE_EXPERIMENTAL_REMOTE_CONTROL=1 后,
// 本地服务经 Kimi 中继暴露到 code-rc.kimi.com,手机/其他设备登录同账号即可控制本机。
// 中继就绪后 CLI 写 ~/.kimi-code/server/rc.json(进程死后残留,须校验 pid)。

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/getlantern/systray"
	webview2 "github.com/jchv/go-webview2"
)

// rcMinVersion 远程操控要求的最低 CLI 版本
var rcMinVersion = [3]int{0, 42, 0}

type rcState struct {
	PID      int    `json:"pid"`
	DeviceID string `json:"device_id"`
	URL      string `json:"url"`
}

// readRCState 读取 rc 锁文件;pid 已死视为无效(文件在进程退出后不会清理)
func readRCState() (rcState, bool) {
	var st rcState
	b, err := os.ReadFile(filepath.Join(os.Getenv("USERPROFILE"), `.kimi-code\server\rc.json`))
	if err != nil || json.Unmarshal(b, &st) != nil || st.PID == 0 || st.URL == "" {
		return rcState{}, false
	}
	if !pidAlive(st.PID) {
		return rcState{}, false
	}
	return st, true
}

// waitRCActive 轮询等待中继通道就绪(重启服务后 rc.json 要几秒才出现)
func waitRCActive(timeout time.Duration) (rcState, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if st, ok := readRCState(); ok {
			return st, true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return rcState{}, false
}

// toggleRC 开关远程操控:改设置并(在本程序拉起的服务上)提示重启生效
func (a *app) toggleRC(item *systray.MenuItem) {
	enable := !getSetting("rc_enabled")
	if enable {
		cur, ok := currentVersion(a.kimi)
		if !ok || versionGreater(rcMinVersion, cur) {
			messageBox("远程操控需要 Kimi Code CLI v0.42.0 及以上。\n请先通过托盘「检查更新」升级。", mbIconWarning)
			return
		}
	}
	setSetting("rc_enabled", enable)
	if enable {
		item.Check()
	} else {
		item.Uncheck()
	}

	if a.cmd == nil {
		// 复用的是外部实例:是否已开 rc 取决于那个进程的启动参数
		if _, ok := readRCState(); ok {
			toastNotify("远程操控已由运行中的实例开启,可在菜单里复制链接")
		} else if enable {
			messageBox("当前复用的是外部启动的 kimi 实例,本程序无法改它的启动参数。\n"+
				"请关闭该实例后点托盘「重启服务」,由本程序拉起并开启远程操控。", mbIconInformation)
		}
		return
	}
	action := "开启"
	if !enable {
		action = "关闭"
	}
	if messageBox(action+"远程操控需要重启 kimi 服务,进行中的任务会中断。\n\n现在重启?",
		mbYesNo|mbIconQuestion) != idYes {
		toastNotify("将在下次重启服务后" + action)
		return
	}
	a.restartService()
	if !enable || a.serviceDead() {
		return // 关闭无需确认;重启失败时 restartService 已弹窗
	}
	if _, ok := waitRCActive(15 * time.Second); ok {
		toastNotify("远程操控已开启,可在托盘菜单复制链接")
	} else {
		messageBox("服务已重启,但未等到远程操控就绪。\n"+
			"该功能需要付费 Kimi 会员;若已开通,请查看日志:\n"+serverLog, mbIconWarning)
	}
}

// copyRCLink 把远程操控链接复制到剪贴板
func copyRCLink() {
	st, ok := readRCState()
	if !ok {
		messageBox("远程操控当前未开启。\n可在托盘「远程操控 → 启用远程操控」打开(需 CLI ≥0.42 与付费会员)。", mbIconInformation)
		return
	}
	if copyToClipboard(st.URL) {
		toastNotify("远程链接已复制到剪贴板")
	} else {
		messageBox("复制失败,链接为:\n"+st.URL, mbIconInformation)
	}
}

// openRCPage 在默认浏览器打开远程操控页面
func openRCPage() {
	st, ok := readRCState()
	if !ok {
		messageBox("远程操控当前未开启。\n可在托盘「远程操控 → 启用远程操控」打开(需 CLI ≥0.42 与付费会员)。", mbIconInformation)
		return
	}
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", st.URL)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	_ = cmd.Start()
}

// showRCQR 弹出二维码窗口(CLI 生成的 ~/.kimi-code/rc-qrcode.png,与 kimi rc 终端里的一致),
// 手机扫码即连;下方附链接文本,便于复制到其他电脑
func showRCQR() {
	st, ok := readRCState()
	if !ok {
		messageBox("远程操控当前未开启。\n可在托盘「远程操控 → 启用远程操控」打开(需 CLI ≥0.42 与付费会员)。", mbIconInformation)
		return
	}
	png, err := os.ReadFile(filepath.Join(os.Getenv("USERPROFILE"), `.kimi-code\rc-qrcode.png`))
	if err != nil {
		messageBox("未找到二维码文件,请直接使用链接:\n"+st.URL, mbIconInformation)
		return
	}
	escaped := strings.ReplaceAll(st.URL, "&", "&amp;")
	html := `<html><body style="margin:0;display:flex;flex-direction:column;align-items:center;` +
		`justify-content:center;height:100vh;font-family:sans-serif;background:#fff;user-select:text">` +
		`<img style="width:360px;height:360px;image-rendering:pixelated" src="data:image/png;base64,` +
		base64.StdEncoding.EncodeToString(png) + `"/>` +
		`<div style="margin-top:18px;color:#444;font-size:16px">手机扫码,登录同一 Kimi 账号即可控制本机</div>` +
		`<div style="margin-top:12px;color:#999;font-size:12px;max-width:88%;word-break:break-all">` + escaped + `</div>` +
		`</body></html>`
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath: webviewDataDir,
		WindowOptions: webview2.WindowOptions{
			Title:  "远程操控 · 扫码连接",
			Width:  480,
			Height: 600,
			Center: true,
			IconId: 1,
		},
	})
	if w == nil {
		messageBox("二维码窗口创建失败,请直接使用链接:\n"+st.URL, mbIconInformation)
		return
	}
	defer w.Destroy()
	w.SetHtml(html)
	w.Run()
}
