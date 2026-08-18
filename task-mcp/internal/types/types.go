// internal/types/types.go — MCP 与 tools 间共享的类型定义
package types

// Tool — MCP 工具接口
type Tool interface {
	Definition() ToolDefinition
	Call(args map[string]interface{}) (string, error)
}

// ToolDefinition MCP 工具元数据
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema ToolInputSchema `json:"inputSchema"`
}

// ToolInputSchema JSON Schema for tool input
type ToolInputSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	Required   []string               `json:"required,omitempty"`
}

// BackendClient 后端 API 客户端接口
type BackendClient interface {
	Do(method, path string, body interface{}) ([]byte, error)
}
