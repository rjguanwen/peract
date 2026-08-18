// cmd/server/main.go — 任务管理 MCP Server 入口
// 通过 stdio 与 AI agent 通信，代理到后端 REST API
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"task-mcp/internal/mcp"
	"task-mcp/internal/types"
)

// 确保 BackendClient 实现了 types.BackendClient 接口
var _ types.BackendClient = (*BackendClient)(nil)

func main() {
	backendURL := getEnv("BACKEND_URL", "http://localhost:8001")
	username := getEnv("MCP_USERNAME", "admin")
	password := getEnv("MCP_PASSWORD", "admin123")

	impl := newBackendClient(backendURL, username, password)
	if err := impl.Authenticate(); err != nil {
		log.Fatalf("认证失败: %v", err)
	}

	var client types.BackendClient = impl
	server := mcp.NewServer(client)
	if err := server.Run(); err != nil {
		log.Fatalf("MCP Server 错误: %v", err)
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ── Backend HTTP Client ─────────────────────────────────────────────────────

type BackendClient struct {
	baseURL    string
	username   string
	password   string
	token      string
	httpClient *http.Client
}

func newBackendClient(baseURL, username, password string) *BackendClient {
	return &BackendClient{
		baseURL:  strings.TrimSuffix(baseURL, "/"),
		username: username,
		password: password,
		httpClient: &http.Client{},
	}
}

func (c *BackendClient) Authenticate() error {
	// 后端 login 接口使用 form-data
	resp, err := c.httpClient.PostForm(c.baseURL+"/api/v1/auth/login", url.Values{
		"username": {c.username},
		"password": {c.password},
	})
	if err != nil {
		return fmt.Errorf("连接后端失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("登录失败 (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}
	c.token = result.AccessToken
	return nil
}

func (c *BackendClient) Do(method, path string, body interface{}) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		bs, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = strings.NewReader(string(bs))
	}

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("请求失败 (HTTP %d): %s", resp.StatusCode, string(data))
	}
	return data, nil
}
