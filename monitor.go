package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type sessionItem struct {
	ID                 string `json:"id"`
	Title              string `json:"title"`
	Busy               bool   `json:"busy"`
	MainTurnActive     bool   `json:"main_turn_active"`
	PendingInteraction string `json:"pending_interaction"`
}

// sessionActivity 单个会话的工作状态
type sessionActivity struct {
	ID      string
	Title   string
	Busy    bool
	Pending bool // 等待用户操作(审批或提问)
}

// fetchActivities 查询所有会话的工作状态;请求失败时 ok=false
func fetchActivities(port int) (list []sessionActivity, ok bool) {
	req, err := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/api/v1/sessions", port), nil)
	if err != nil {
		return nil, false
	}
	if t := readToken(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, false
	}
	var data struct {
		Data struct {
			Items []sessionItem `json:"items"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &data) != nil {
		return nil, false
	}
	for _, it := range data.Data.Items {
		list = append(list, sessionActivity{
			ID:      it.ID,
			Title:   it.Title,
			Busy:    it.Busy || it.MainTurnActive,
			Pending: it.PendingInteraction != "" && it.PendingInteraction != "none",
		})
	}
	return list, true
}
