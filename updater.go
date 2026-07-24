package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"time"
)

var versionRe = regexp.MustCompile(`\d+`)

// parseVersion 从字符串里提取 [major, minor, patch] 数字版本号
func parseVersion(text string) [3]int {
	var v [3]int
	for i, n := range versionRe.FindAllString(text, 3) {
		v[i], _ = strconv.Atoi(n)
	}
	return v
}

func currentVersion(kimi string) ([3]int, bool) {
	cmd := exec.Command(kimi, "--version")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	out, err := cmd.Output()
	if err != nil {
		return [3]int{}, false
	}
	return parseVersion(string(out)), true
}

// latestVersion 查询最新版本(npm registry,失败走国内镜像)
func latestVersion() ([3]int, bool) {
	for _, url := range npmLatestURLs {
		client := &http.Client{Timeout: 6 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if err != nil {
			continue
		}
		var data struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(body, &data) == nil && data.Version != "" {
			return parseVersion(data.Version), true
		}
	}
	return [3]int{}, false
}

var checkLock sync.Mutex

// checkForUpdates 检测 Kimi Code CLI 更新(每 24h 最多一次,离线也计入;
// force 跳过间隔与跳过记录并给出反馈)。带可重入保护,防止托盘连点导致弹窗排队
func checkForUpdates(kimi string, force bool) {
	if !checkLock.TryLock() {
		return
	}
	defer checkLock.Unlock()

	s := loadState()
	lastCheck, _ := s["last_check"].(float64)
	if !force && time.Since(time.Unix(int64(lastCheck), 0)) < checkInterval {
		return
	}
	newVer, ok := latestVersion()
	s["last_check"] = float64(time.Now().Unix())
	saveState(s)
	if !ok {
		if force {
			messageBox("暂时无法检查更新:网络不可用。", mbIconWarning)
		}
		return
	}
	cur, ok := currentVersion(kimi)
	if !ok || !versionGreater(newVer, cur) {
		if force {
			v := cur
			if !ok {
				v = newVer
			}
			messageBox(fmt.Sprintf("已是最新版本 v%d.%d.%d。", v[0], v[1], v[2]), mbIconInformation)
		}
		return
	}
	newStr := fmt.Sprintf("%d.%d.%d", newVer[0], newVer[1], newVer[2])
	if !force && s["skip_version"] == newStr {
		return
	}
	answer := messageBox(
		fmt.Sprintf("检测到 Kimi Code CLI 新版本 v%s(当前 v%d.%d.%d)。\n\n是:立即升级\n否:本次跳过\n取消:跳过此版本,不再提示",
			newStr, cur[0], cur[1], cur[2]),
		mbYesNoCancel|mbIconQuestion,
	)
	if answer == idCancel {
		s["skip_version"] = newStr
		saveState(s)
		return
	}
	if answer != idYes {
		return
	}
	// 官方 Windows 安装/升级脚本;开可见控制台窗口显示下载进度
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", upgradeCommand)
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	code := 0
	if err := cmd.Run(); err != nil {
		code = 1
	}
	if code == 0 {
		delete(s, "skip_version")
		saveState(s)
		messageBox("升级完成,将以新版本启动。", mbIconInformation)
	} else {
		messageBox("升级未完成(可能被取消或网络失败),将继续使用当前版本。", mbIconWarning)
	}
}

func versionGreater(a, b [3]int) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}
