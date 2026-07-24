package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ---- kimi 可执行文件与 token ----

func findKimi() string {
	if p, err := exec.LookPath("kimi"); err == nil {
		return p
	}
	def := filepath.Join(os.Getenv("USERPROFILE"), `.kimi-code\bin\kimi.exe`)
	if _, err := os.Stat(def); err == nil {
		return def
	}
	return ""
}

func readToken() string {
	b, err := os.ReadFile(filepath.Join(os.Getenv("USERPROFILE"), ".kimi-code", "server.token"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ---- 服务发现 ----

var httpClient = &http.Client{Timeout: 3 * time.Second}

// probeServer 探测指定端口是否是 kimi web:用 /openapi.json 做指纹识别
func probeServer(port int) bool {
	req, err := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/openapi.json", port), nil)
	if err != nil {
		return false
	}
	if t := readToken(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return false
	}
	return strings.Contains(string(body), `"openapi"`)
}

type instanceInfo struct {
	PID  int    `json:"pid"`
	Host string `json:"host"`
	Port int    `json:"port"`
}

// findRunningServer 从 kimi 的实例注册表找一个活着的 kimi web 实例
// (校验 pid 存活 + openapi 指纹),返回其端口;没有则返回 0
func findRunningServer() int {
	dir := filepath.Join(os.Getenv("USERPROFILE"), `.kimi-code\server\instances`)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	type item struct {
		info    instanceInfo
		modTime time.Time
	}
	var items []item
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var ii instanceInfo
		if json.Unmarshal(b, &ii) != nil {
			continue
		}
		items = append(items, item{ii, fi.ModTime()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].modTime.After(items[j].modTime) })
	for _, it := range items {
		if it.info.Host != "127.0.0.1" || it.info.PID == 0 || it.info.Port == 0 {
			continue
		}
		if pidAlive(it.info.PID) && probeServer(it.info.Port) {
			return it.info.Port
		}
	}
	return 0
}

// pickPort 优先使用固定端口(保持 localStorage 有效);被占用则顺延
func pickPort() int {
	for port := preferredPort; port < preferredPort+portScanLimit; port++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			_ = ln.Close()
			return port
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return preferredPort
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// waitForServer 轮询直到服务就绪(openapi 指纹)。
// kimi web 在端口被占时会自动 +1,所以探测一小段连续端口。返回实际端口,失败返回 0
func waitForServer(port int, cmd *exec.Cmd) int {
	deadline := time.Now().Add(startupTimeout)
	for time.Now().Before(deadline) {
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return 0
		}
		for p := port; p < port+5; p++ {
			if probeServer(p) {
				return p
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return 0
}

// spawnService 隐藏窗口拉起 kimi web,输出写入日志便于诊断
func spawnService(kimi string, port int, logFile **os.File) (*exec.Cmd, error) {
	_ = os.MkdirAll(dataDir, 0o755)
	if *logFile == nil {
		f, err := os.Create(serverLog)
		if err != nil {
			return nil, err
		}
		*logFile = f
	}
	cmd := exec.Command(kimi, "web", "--no-open", "--port", strconv.Itoa(port))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	cmd.Stdout = *logFile
	cmd.Stderr = *logFile
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// runDoctor 服务启动失败时运行 kimi doctor,把配置校验结果附加到日志
func runDoctor(kimi string) {
	cmd := exec.Command(kimi, "doctor")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return
	}
	f, err := os.OpenFile(serverLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString("\n--- kimi doctor ---\n" + string(out) + "\n")
}
