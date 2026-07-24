package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const (
	appTitle       = "Kimi Web"
	preferredPort  = 58627 // kimi web 默认端口
	portScanLimit  = 20
	startupTimeout = 60 * time.Second
	checkInterval  = 24 * time.Hour // 更新检测间隔

	createNoWindow = 0x08000000

	upgradeCommand = "irm https://code.kimi.com/kimi-code/install.ps1 | iex"
	webview2GUID   = `{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`
)

var npmLatestURLs = []string{
	"https://registry.npmjs.org/@moonshot-ai/kimi-code/latest",
	"https://registry.npmmirror.com/@moonshot-ai/kimi-code/latest",
}

var (
	dataDir        = filepath.Join(os.Getenv("LOCALAPPDATA"), "KimiWeb")
	stateFile      = filepath.Join(dataDir, "state.json")
	serverLog      = filepath.Join(dataDir, "kimi-web.log")
	webviewDataDir = filepath.Join(dataDir, "EBWebView") // 与 Python 版同一 profile,引导状态互通
)

// state 与 Python 版 state.json 格式兼容(last_check/skip_version/anim_busy/notify_toast/notify_sound)
type state map[string]any

func loadState() state {
	s := state{}
	b, err := os.ReadFile(stateFile)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(b, &s)
	return s
}

func saveState(s state) {
	_ = os.MkdirAll(dataDir, 0o755)
	b, err := json.Marshal(s)
	if err != nil {
		return
	}
	_ = os.WriteFile(stateFile, b, 0o644)
}

var settingDefaults = map[string]bool{
	"anim_busy":    true, // 工作时标题与托盘动画
	"notify_toast": true, // 完成时 Windows 通知
	"notify_sound": false, // 完成时提示音
}

func getSetting(key string) bool {
	v, ok := loadState()[key].(bool)
	if !ok {
		return settingDefaults[key]
	}
	return v
}

func setSetting(key string, value bool) {
	s := loadState()
	s[key] = value
	saveState(s)
}
