// internal/mcp/server.go — MCP 协议层（stdio JSON-RPC）
package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"

	"task-mcp/internal/tools"
	"task-mcp/internal/types"
)

// JSON-RPC message types
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      interface{}     `json:"id,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
	ID      interface{} `json:"id,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolsListResult struct {
	Tools []types.ToolDefinition `json:"tools"`
}

type toolsCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

type toolsCallResult struct {
	Content []toolResultContent `json:"content"`
	IsError bool                `json:"isError,omitempty"`
}

type toolResultContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Server — MCP stdio 服务器
type Server struct {
	client types.BackendClient
	tools  map[string]types.Tool
	mu     sync.Mutex
}

func NewServer(client types.BackendClient) *Server {
	s := &Server{client: client}
	s.tools = tools.RegisterAll(s.client)
	return s
}

func (s *Server) Run() error {
	reader := bufio.NewReader(os.Stdin)
	writer := os.Stdout

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return fmt.Errorf("读取 stdin 失败: %w", err)
		}
		// EOF 时仍可能带着一段没有结尾换行的数据，直接 return 会丢掉最后一条请求
		if err == io.EOF && len(line) == 0 {
			return nil
		}

		lineStr := strings.TrimSpace(string(line))
		if lineStr == "" {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal([]byte(lineStr), &req); err != nil {
			s.sendError(writer, nil, -32700, "解析错误: "+err.Error())
			continue
		}

		// 通知（无 id）一律不回应，这是 JSON-RPC 与 MCP 共同的约定
		if req.ID == nil {
			continue
		}

		s.mu.Lock()
		err = s.handle(req, writer)
		s.mu.Unlock()
		if err != nil {
			s.sendError(writer, req.ID, -32603, err.Error())
		}
	}
}

func (s *Server) handle(req jsonRPCRequest, writer io.Writer) error {
	switch req.Method {
	case "initialize":
		result := struct {
			ProtocolVersion string                 `json:"protocolVersion"`
			Capabilities    map[string]interface{} `json:"capabilities"`
			ServerInfo      struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
		}{
			ProtocolVersion: "2024-11-05",
			Capabilities:    map[string]interface{}{"tools": map[string]interface{}{}},
			ServerInfo: struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			}{Name: "task-mcp", Version: "1.0.0"},
		}
		return s.sendResult(writer, req.ID, result)

	// ping 是客户端的心跳探活，不回应会被当作服务不可用而断开
	case "ping":
		return s.sendResult(writer, req.ID, struct{}{})

	case "tools/list":
		defs := make([]types.ToolDefinition, 0, len(s.tools))
		for _, t := range s.tools {
			defs = append(defs, t.Definition())
		}
		// 固定排序：map 遍历顺序随机，不排序的话每次 tools/list 返回的工具顺序都不同，
		// 会破坏客户端缓存与提示词前缀复用
		sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
		return s.sendResult(writer, req.ID, toolsListResult{Tools: defs})

	case "tools/call":
		var params toolsCallParams
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return s.sendError(writer, req.ID, -32602, "params 解析失败: "+err.Error())
			}
		}
		tool, ok := s.tools[params.Name]
		if !ok {
			return s.sendError(writer, req.ID, -32602, fmt.Sprintf("未知工具: %s", params.Name))
		}
		result, err := tool.Call(params.Arguments)
		if err != nil {
			return s.sendResult(writer, req.ID, toolsCallResult{
				Content: []toolResultContent{{Type: "text", Text: "错误: " + err.Error()}},
				IsError: true,
			})
		}
		return s.sendResult(writer, req.ID, toolsCallResult{
			Content: []toolResultContent{{Type: "text", Text: result}},
		})

	default:
		return s.sendError(writer, req.ID, -32601, fmt.Sprintf("未知方法: %s", req.Method))
	}
}

func (s *Server) sendResult(w io.Writer, id interface{}, result interface{}) error {
	resp := jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: result}
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

func (s *Server) sendError(w io.Writer, id interface{}, code int, msg string) error {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: msg},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}
