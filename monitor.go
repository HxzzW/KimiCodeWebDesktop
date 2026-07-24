package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type sessionItem struct {
	Busy               bool   `json:"busy"`
	MainTurnActive     bool   `json:"main_turn_active"`
	PendingInteraction string `json:"pending_interaction"`
}

// fetchActivity 查询所有会话的工作状态。
// 返回 (是否有会话在忙, 是否有会话等待交互, 请求是否成功)
func fetchActivity(port int) (bool, bool, bool) {
	req, err := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/api/v1/sessions", port), nil)
	if err != nil {
		return false, false, false
	}
	if t := readToken(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return false, false, false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return false, false, false
	}
	var data struct {
		Data struct {
			Items []sessionItem `json:"items"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &data) != nil {
		return false, false, false
	}
	busy := false
	pending := false
	for _, it := range data.Data.Items {
		if it.Busy || it.MainTurnActive {
			busy = true
		}
		if it.PendingInteraction != "" && it.PendingInteraction != "none" {
			pending = true
		}
	}
	return busy, pending, true
}
