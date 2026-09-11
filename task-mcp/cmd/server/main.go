// cmd/server/main.go — 任务管理 MCP Server 入口
// 通过 stdio 与 AI agent 通信，代理到后端 REST API
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"task-mcp/internal/mcp"
	"task-mcp/internal/types"
)

// 确保 BackendClient 实现了 types.BackendClient 接口
var _ types.BackendClient = (*BackendClient)(nil)

// maxResponseBytes 单次后端响应的读取上限，防止后端异常时把 MCP 进程内存吃光
const maxResponseBytes = 8 << 20

func main() {
	backendURL := getEnv("BACKEND_URL", "http://localhost:8001")
	if err := validateBackendURL(backendURL); err != nil {
		log.Fatalf("BACKEND_URL 不合法: %v", err)
	}

	// 凭据一律要求显式提供。原先的 admin/admin123 默认值会把「忘记配置」
	// 变成一个能静默读写全部任务数据的高权限会话。
	username := os.Getenv("MCP_USERNAME")
	password := os.Getenv("MCP_PASSWORD")
	if username == "" || password == "" {
		log.Fatal("必须设置 MCP_USERNAME 与 MCP_PASSWORD 环境变量（建议使用仅含所需权限的专用账号）")
	}

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

// validateBackendURL 只接受 http/https 绝对地址，把配置错误在启动时报清，
// 而不是等到第一个工具调用才表现为一系列模糊的请求失败。
func validateBackendURL(raw string) error {
	u, err := url.Parse(strings.TrimSuffix(raw, "/"))
	if err != nil {
		return err
	}
	if u.Host == "" {
		return fmt.Errorf("%q 缺少主机名，应形如 http://localhost:8001", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("不支持的协议 %q，仅支持 http/https", u.Scheme)
	}
	return nil
}

// ── Backend HTTP Client ─────────────────────────────────────────────────────

type BackendClient struct {
	baseURL  string
	username string
	password string

	mu         sync.Mutex // 保护 token：重登与读取不能交叉
	token      string
	httpClient *http.Client
}

func newBackendClient(baseURL, username, password string) *BackendClient {
	return &BackendClient{
		baseURL:  strings.TrimSuffix(baseURL, "/"),
		username: username,
		password: password,
		// 没有超时的 http.Client 会一直等下去：后端卡住 = MCP 会话永久挂起
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        20,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     60 * time.Second,
			},
		},
	}
}

func (c *BackendClient) Authenticate() error {
	// 后端 login 接口使用 form-data
	form := url.Values{"username": {c.username}, "password": {c.password}}
	resp, err := c.httpClient.PostForm(c.baseURL+"/api/v1/auth/login", form)
	if err != nil {
		return fmt.Errorf("连接后端失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := readCapped(resp.Body)
		return fmt.Errorf("登录失败 (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}
	if result.AccessToken == "" {
		return errors.New("登录响应中没有 access_token")
	}
	c.mu.Lock()
	c.token = result.AccessToken
	c.mu.Unlock()
	return nil
}

// currentToken 读取当前令牌快照。
func (c *BackendClient) currentToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

// Do 发起一次后端调用。令牌过期（401）时自动重登并重试一次。
//
// 访问令牌有有效期，而 MCP Server 是长驻进程——不重登的话，
// 服务放一晚上之后所有工具调用都会以 401 失败。
func (c *BackendClient) Do(method, path string, body any) ([]byte, error) {
	data, status, err := c.doOnce(method, path, body)
	if errors.Is(err, errUnauthorized) {
		if reauthErr := c.Authenticate(); reauthErr != nil {
			return nil, fmt.Errorf("凭证已过期且重新登录失败: %w", reauthErr)
		}
		data, status, err = c.doOnce(method, path, body)
	}
	if errors.Is(err, errUnauthorized) {
		return nil, fmt.Errorf("请求被拒绝 (HTTP %d)：重新登录后仍无权限，请检查 MCP 账号的权限设置", status)
	}
	return data, err
}

var errUnauthorized = errors.New("unauthorized")

func (c *BackendClient) doOnce(method, path string, body any) ([]byte, int, error) {
	var payload []byte
	if body != nil {
		bs, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("序列化请求体失败: %w", err)
		}
		payload = bs
	}

	target := c.baseURL + path
	if _, err := url.Parse(target); err != nil {
		return nil, 0, fmt.Errorf("请求地址不合法: %w", err)
	}
	req, err := http.NewRequest(method, target, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token := c.currentToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	data, err := readCapped(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, resp.StatusCode, errUnauthorized
	}
	if resp.StatusCode >= 400 {
		return nil, resp.StatusCode, fmt.Errorf("请求失败 (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, resp.StatusCode, nil
}

// readCapped 读取响应体，超出上限即报错。
func readCapped(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxResponseBytes {
		return nil, fmt.Errorf("后端响应超过 %d 字节上限", maxResponseBytes)
	}
	return data, nil
}
