// internal/tools/tools.go — 所有 MCP 工具注册与实现
package tools

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"task-mcp/internal/types"
)

// Tool — MCP 工具接口（来自 types 包）
type Tool = types.Tool

// Backend client interface (来自 types 包)
type backendClient = types.BackendClient

// RegisterAll — 注册全部工具
func RegisterAll(client backendClient) map[string]Tool {
	t := map[string]Tool{
		// ── 任务工具 ────────────────────────────────────────
		"create_task":       &CreateTask{client: client},
		"list_tasks":        &ListTasks{client: client},
		"get_task":          &GetTask{client: client},
		"update_task":       &UpdateTask{client: client},
		"delete_task":       &DeleteTask{client: client},
		"restore_task":      &RestoreTask{client: client},
		"list_deleted_tasks": &ListDeletedTasks{client: client},
		"add_progress":      &AddProgress{client: client},
		// ── 用户工具 ────────────────────────────────────────
		"list_users":   &ListUsers{client: client},
		"get_me":       &GetMe{client: client},
		// ── 提醒工具 ────────────────────────────────────────
		"list_reminders":     &ListReminders{client: client},
		"create_reminder":    &CreateReminder{client: client},
		"mark_reminder_read": &MarkReminderRead{client: client},
		// ── 统计工具 ────────────────────────────────────────
		"get_stats": &GetStats{client: client},
	}
	return t
}

// ── 任务工具 ──────────────────────────────────────────────────────────────

type CreateTask struct{ client backendClient }

func (t *CreateTask) Definition() types.ToolDefinition {
	return 	types.ToolDefinition{
		Name:        "create_task",
		Description: "创建新任务。需要：标题、描述（可选）、截止日期（可选，格式 YYYY-MM-DD）、优先级（low/medium/high/urgent）、负责人用户名（可选）",
		InputSchema: types.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"title":        map[string]interface{}{"type": "string", "description": "任务标题（必填）"},
				"description":  map[string]interface{}{"type": "string", "description": "任务描述"},
				"due_date":     map[string]interface{}{"type": "string", "description": "截止日期，格式 YYYY-MM-DD"},
				"priority":     map[string]interface{}{"type": "string", "enum": []string{"low", "medium", "high", "urgent"}, "description": "优先级"},
				"assignee":     map[string]interface{}{"type": "string", "description": "负责人用户名（可选）"},
			},
			Required: []string{"title"},
		},
	}
}

func (t *CreateTask) Call(args map[string]interface{}) (string, error) {
	body := map[string]interface{}{}
	setString(body, args, "title", "title")
	setString(body, args, "description", "description")
	setString(body, args, "priority", "priority")
	if dueDate, ok := args["due_date"].(string); ok && dueDate != "" {
		body["due_date"] = dueDate + "T00:00:00"
	}
	if assignee, ok := args["assignee"].(string); ok && assignee != "" {
		// 先查用户ID
		data, err := t.client.Do("GET", "/api/v1/users", nil)
		if err != nil {
			return "", fmt.Errorf("查询用户列表失败: %w", err)
		}
		var users struct {
			Data []struct {
				ID       uint   `json:"id"`
				Username string `json:"username"`
			} `json:"data"`
		}
		json.Unmarshal(data, &users)
		for _, u := range users.Data {
			if u.Username == assignee {
				body["assignee_id"] = u.ID
				break
			}
		}
	}
	data, err := t.client.Do("POST", "/api/v1/tasks", body)
	if err != nil {
		return "", err
	}
	return prettyJSON(data)
}

type ListTasks struct{ client backendClient }

func (t *ListTasks) Definition() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "list_tasks",
		Description: "查询任务列表，支持按状态/优先级/负责人筛选",
		InputSchema: types.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"status":    map[string]interface{}{"type": "string", "enum": []string{"todo", "in_progress", "done"}, "description": "状态筛选"},
				"priority":  map[string]interface{}{"type": "string", "enum": []string{"low", "medium", "high", "urgent"}, "description": "优先级筛选"},
				"assignee":  map[string]interface{}{"type": "string", "description": "负责人用户名筛选"},
				"creator":   map[string]interface{}{"type": "string", "description": "创建者用户名筛选"},
				"page":      map[string]interface{}{"type": "integer", "description": "页码（默认1）"},
				"page_size": map[string]interface{}{"type": "integer", "description": "每页数量（默认20）"},
			},
		},
	}
}

func (t *ListTasks) Call(args map[string]interface{}) (string, error) {
	path := "/api/v1/tasks?"
	if v, ok := args["status"].(string); ok && v != "" {
		path += "status=" + v + "&"
	}
	if v, ok := args["priority"].(string); ok && v != "" {
		path += "priority=" + v + "&"
	}
	if v, ok := args["assignee"].(string); ok && v != "" {
		path += "assignee=" + v + "&"
	}
	if v, ok := args["creator"].(string); ok && v != "" {
		path += "creator=" + v + "&"
	}
	if v, ok := args["page"].(float64); ok && v > 0 {
		path += "page=" + strconv.Itoa(int(v)) + "&"
	}
	if v, ok := args["page_size"].(float64); ok && v > 0 {
		path += "page_size=" + strconv.Itoa(int(v)) + "&"
	}
	data, err := t.client.Do("GET", path, nil)
	if err != nil {
		return "", err
	}
	return prettyJSON(data)
}

type GetTask struct{ client backendClient }

func (t *GetTask) Definition() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "get_task",
		Description: "根据 ID 获取单个任务详情，包括进展记录",
		InputSchema: types.ToolInputSchema{
			Type:       "object",
			Properties: map[string]interface{}{"task_id": map[string]interface{}{"type": "integer", "description": "任务 ID"}},
			Required:   []string{"task_id"},
		},
	}
}

func (t *GetTask) Call(args map[string]interface{}) (string, error) {
	id := intArg(args, "task_id")
	data, err := t.client.Do("GET", fmt.Sprintf("/api/v1/tasks/%d", id), nil)
	if err != nil {
		return "", err
	}
	return prettyJSON(data)
}

type UpdateTask struct{ client backendClient }

func (t *UpdateTask) Definition() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "update_task",
		Description: "更新任务信息（标题/描述/状态/优先级/进度/截止日期/负责人）。权限：编辑基本信息需创建者或管理员；状态/进度流转允许负责人",
		InputSchema: types.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"task_id":    map[string]interface{}{"type": "integer", "description": "任务 ID（必填）"},
				"title":      map[string]interface{}{"type": "string", "description": "新标题"},
				"description": map[string]interface{}{"type": "string", "description": "新描述"},
				"status":     map[string]interface{}{"type": "string", "enum": []string{"todo", "in_progress", "done"}, "description": "新状态"},
				"priority":   map[string]interface{}{"type": "string", "enum": []string{"low", "medium", "high", "urgent"}, "description": "新优先级"},
				"progress":   map[string]interface{}{"type": "integer", "description": "进度 0-100"},
				"due_date":   map[string]interface{}{"type": "string", "description": "截止日期 YYYY-MM-DD"},
				"assignee":   map[string]interface{}{"type": "string", "description": "负责人用户名"},
			},
			Required: []string{"task_id"},
		},
	}
}

func (t *UpdateTask) Call(args map[string]interface{}) (string, error) {
	id := intArg(args, "task_id")
	body := map[string]interface{}{}
	setString(body, args, "title", "title")
	setString(body, args, "description", "description")
	setString(body, args, "status", "status")
	setString(body, args, "priority", "priority")
	if v, ok := args["progress"]; ok {
		if f, ok := v.(float64); ok {
			body["progress"] = int(f)
		}
	}
	if dueDate, ok := args["due_date"].(string); ok && dueDate != "" {
		body["due_date"] = dueDate + "T00:00:00"
	}
	if assignee, ok := args["assignee"].(string); ok && assignee != "" {
		data, _ := t.client.Do("GET", "/api/v1/users", nil)
		var users struct {
			Data []struct {
				ID       uint   `json:"id"`
				Username string `json:"username"`
			} `json:"data"`
		}
		json.Unmarshal(data, &users)
		for _, u := range users.Data {
			if u.Username == assignee {
				body["assignee_id"] = u.ID
				break
			}
		}
	}
	data, err := t.client.Do("PATCH", fmt.Sprintf("/api/v1/tasks/%d", id), body)
	if err != nil {
		return "", err
	}
	return prettyJSON(data)
}

type DeleteTask struct{ client backendClient }

func (t *DeleteTask) Definition() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "delete_task",
		Description: "软删除任务（放入回收站）。权限：仅创建者或管理员可执行",
		InputSchema: types.ToolInputSchema{
			Type:       "object",
			Properties: map[string]interface{}{"task_id": map[string]interface{}{"type": "integer", "description": "任务 ID"}},
			Required:   []string{"task_id"},
		},
	}
}

func (t *DeleteTask) Call(args map[string]interface{}) (string, error) {
	id := intArg(args, "task_id")
	data, err := t.client.Do("DELETE", fmt.Sprintf("/api/v1/tasks/%d", id), nil)
	if err != nil {
		return "", err
	}
	return prettyJSON(data)
}

type RestoreTask struct{ client backendClient }

func (t *RestoreTask) Definition() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "restore_task",
		Description: "从回收站恢复任务。权限：仅创建者或管理员可执行",
		InputSchema: types.ToolInputSchema{
			Type:       "object",
			Properties: map[string]interface{}{"task_id": map[string]interface{}{"type": "integer", "description": "任务 ID"}},
			Required:   []string{"task_id"},
		},
	}
}

func (t *RestoreTask) Call(args map[string]interface{}) (string, error) {
	id := intArg(args, "task_id")
	data, err := t.client.Do("POST", fmt.Sprintf("/api/v1/tasks/%d/restore", id), nil)
	if err != nil {
		return "", err
	}
	return prettyJSON(data)
}

type ListDeletedTasks struct{ client backendClient }

func (t *ListDeletedTasks) Definition() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "list_deleted_tasks",
		Description: "查看回收站中的已删除任务列表",
		InputSchema: types.ToolInputSchema{Type: "object", Properties: map[string]interface{}{}},
	}
}

func (t *ListDeletedTasks) Call(_ map[string]interface{}) (string, error) {
	data, err := t.client.Do("GET", "/api/v1/tasks/deleted", nil)
	if err != nil {
		return "", err
	}
	return prettyJSON(data)
}

type AddProgress struct{ client backendClient }

func (t *AddProgress) Definition() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "add_progress",
		Description: "为任务添加进展记录",
		InputSchema: types.ToolInputSchema{
			Type:       "object",
			Properties: map[string]interface{}{
				"task_id": map[string]interface{}{"type": "integer", "description": "任务 ID"},
				"content": map[string]interface{}{"type": "string", "description": "进展内容"},
				"images":  map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "图片URL列表（可选）"},
			},
			Required: []string{"task_id", "content"},
		},
	}
}

func (t *AddProgress) Call(args map[string]interface{}) (string, error) {
	id := intArg(args, "task_id")
	content, _ := args["content"].(string)
	images, _ := args["images"].([]interface{})

	imgList := make([]string, 0, len(images))
	for _, img := range images {
		if s, ok := img.(string); ok {
			imgList = append(imgList, s)
		}
	}

	body := map[string]interface{}{
		"content":     content,
		"images":      imgList,
		"record_time": time.Now().Format("2006-01-02 15:04:05"),
	}
	data, err := t.client.Do("POST", fmt.Sprintf("/api/v1/tasks/%d/progress", id), body)
	if err != nil {
		return "", err
	}
	return prettyJSON(data)
}

// ── 用户工具 ──────────────────────────────────────────────────────────────

type ListUsers struct{ client backendClient }

func (t *ListUsers) Definition() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "list_users",
		Description: "查询所有用户列表",
		InputSchema: types.ToolInputSchema{Type: "object", Properties: map[string]interface{}{}},
	}
}

func (t *ListUsers) Call(_ map[string]interface{}) (string, error) {
	data, err := t.client.Do("GET", "/api/v1/users", nil)
	if err != nil {
		return "", err
	}
	return prettyJSON(data)
}

type GetMe struct{ client backendClient }

func (t *GetMe) Definition() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "get_me",
		Description: "获取当前登录用户的详细信息",
		InputSchema: types.ToolInputSchema{Type: "object", Properties: map[string]interface{}{}},
	}
}

func (t *GetMe) Call(_ map[string]interface{}) (string, error) {
	data, err := t.client.Do("GET", "/api/v1/auth/me", nil)
	if err != nil {
		return "", err
	}
	return prettyJSON(data)
}

// ── 提醒工具 ──────────────────────────────────────────────────────────────

type ListReminders struct{ client backendClient }

func (t *ListReminders) Definition() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "list_reminders",
		Description: "查看当前用户的提醒列表（支持未读筛选）",
		InputSchema: types.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"unread_only": map[string]interface{}{"type": "boolean", "description": "仅显示未读"},
			},
		},
	}
}

func (t *ListReminders) Call(args map[string]interface{}) (string, error) {
	path := "/api/v1/reminders?"
	if v, ok := args["unread_only"].(bool); ok && v {
		path += "unread=true&"
	}
	data, err := t.client.Do("GET", path, nil)
	if err != nil {
		return "", err
	}
	return prettyJSON(data)
}

type CreateReminder struct{ client backendClient }

func (t *CreateReminder) Definition() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "create_reminder",
		Description: "为任务创建提醒通知",
		InputSchema: types.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"task_id":    map[string]interface{}{"type": "integer", "description": "关联任务 ID"},
				"remind_at":  map[string]interface{}{"type": "string", "description": "提醒时间，格式 YYYY-MM-DD HH:MM:SS"},
				"remind_type": map[string]interface{}{"type": "string", "enum": []string{"email", "webhook", "both"}, "description": "提醒方式"},
				"message":    map[string]interface{}{"type": "string", "description": "自定义提醒消息（可选）"},
			},
			Required: []string{"task_id", "remind_at"},
		},
	}
}

func (t *CreateReminder) Call(args map[string]interface{}) (string, error) {
	body := map[string]interface{}{}
	setInt(body, args, "task_id", "task_id")
	setString(body, args, "remind_at", "remind_at")
	setString(body, args, "remind_type", "remind_type")
	setString(body, args, "message", "message")
	data, err := t.client.Do("POST", "/api/v1/reminders", body)
	if err != nil {
		return "", err
	}
	return prettyJSON(data)
}

type MarkReminderRead struct{ client backendClient }

func (t *MarkReminderRead) Definition() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "mark_reminder_read",
		Description: "标记提醒为已读",
		InputSchema: types.ToolInputSchema{
			Type:       "object",
			Properties: map[string]interface{}{"reminder_id": map[string]interface{}{"type": "integer", "description": "提醒 ID"}},
			Required:   []string{"reminder_id"},
		},
	}
}

func (t *MarkReminderRead) Call(args map[string]interface{}) (string, error) {
	id := intArg(args, "reminder_id")
	data, err := t.client.Do("POST", fmt.Sprintf("/api/v1/reminders/%d/read", id), nil)
	if err != nil {
		return "", err
	}
	return prettyJSON(data)
}

// ── 统计工具 ──────────────────────────────────────────────────────────────

type GetStats struct{ client backendClient }

func (t *GetStats) Definition() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "get_stats",
		Description: "获取仪表盘统计数据（任务总数/状态分布/逾期数/本周新增等）",
		InputSchema: types.ToolInputSchema{Type: "object", Properties: map[string]interface{}{}},
	}
}

func (t *GetStats) Call(_ map[string]interface{}) (string, error) {
	data, err := t.client.Do("GET", "/api/v1/stats/overview", nil)
	if err != nil {
		return "", err
	}
	return prettyJSON(data)
}

// ── 辅助函数 ──────────────────────────────────────────────────────────────

func setString(dst map[string]interface{}, src map[string]interface{}, srcKey, dstKey string) {
	if v, ok := src[srcKey].(string); ok && v != "" {
		dst[dstKey] = v
	}
}

func setInt(dst map[string]interface{}, src map[string]interface{}, srcKey, dstKey string) {
	if v, ok := src[srcKey].(float64); ok {
		dst[dstKey] = int(v)
	}
}

func intArg(args map[string]interface{}, key string) int {
	if v, ok := args[key].(float64); ok {
		return int(v)
	}
	return 0
}

func prettyJSON(data []byte) (string, error) {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return string(data), nil
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(data), nil
	}
	return string(out), nil
}
