// internal/tools/tools.go — 所有 MCP 工具注册与实现
package tools

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
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
		"create_task":        &CreateTask{client: client},
		"list_tasks":         &ListTasks{client: client},
		"get_task":           &GetTask{client: client},
		"update_task":        &UpdateTask{client: client},
		"delete_task":        &DeleteTask{client: client},
		"restore_task":       &RestoreTask{client: client},
		"list_deleted_tasks": &ListDeletedTasks{client: client},
		"add_progress":       &AddProgress{client: client},
		// ── 用户工具 ────────────────────────────────────────
		"list_users": &ListUsers{client: client},
		"get_me":     &GetMe{client: client},
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
	return types.ToolDefinition{
		Name:        "create_task",
		Description: "创建新任务。需要：标题、描述（可选）、截止日期（可选，格式 YYYY-MM-DDTHH:MM:SS 或 YYYY-MM-DD）、优先级（low/medium/high/urgent）、负责人用户名（可选）",
		InputSchema: types.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"title":       map[string]interface{}{"type": "string", "description": "任务标题（必填）"},
				"description": map[string]interface{}{"type": "string", "description": "任务描述"},
				"due_date":    map[string]interface{}{"type": "string", "description": "截止时间，YYYY-MM-DD 或 YYYY-MM-DDTHH:MM:SS"},
				"priority":    map[string]interface{}{"type": "string", "enum": []string{"low", "medium", "high", "urgent"}, "description": "优先级"},
				"assignee":    map[string]interface{}{"type": "string", "description": "负责人用户名（可选）"},
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
	if due, err := parseDueDate(args["due_date"]); err != nil {
		return "", err
	} else if due != "" {
		body["due_date"] = due
	}
	if name := usernameArg(args); name != "" {
		id, err := resolveUserID(t.client, name)
		if err != nil {
			return "", err
		}
		body["assignee_id"] = id
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
		Description: "查询任务列表，支持按状态/优先级/负责人/关键词筛选与分页",
		InputSchema: types.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"status":       map[string]interface{}{"type": "string", "enum": []string{"todo", "in_progress", "done"}, "description": "状态筛选"},
				"priority":     map[string]interface{}{"type": "string", "enum": []string{"low", "medium", "high", "urgent"}, "description": "优先级筛选"},
				"assignee":     map[string]interface{}{"type": "string", "description": "负责人用户名（先解析为 ID 再查询）"},
				"keyword":      map[string]interface{}{"type": "string", "description": "标题/描述关键词"},
				"mine":         map[string]interface{}{"type": "boolean", "description": "仅看我负责的"},
				"overdue_only": map[string]interface{}{"type": "boolean", "description": "仅看逾期未完成"},
				"page":         map[string]interface{}{"type": "integer", "description": "页码（默认1）"},
				"page_size":    map[string]interface{}{"type": "integer", "description": "每页数量（默认20，上限100）"},
			},
		},
	}
}

func (t *ListTasks) Call(args map[string]interface{}) (string, error) {
	q := url.Values{}
	setQuery(q, args, "status", "status")
	setQuery(q, args, "priority", "priority")
	setQuery(q, args, "keyword", "keyword")
	if name := usernameArg(args); name != "" {
		id, err := resolveUserID(t.client, name)
		if err != nil {
			return "", err
		}
		q.Set("assignee_id", strconv.FormatUint(uint64(id), 10))
	}
	if v, ok := args["mine"].(bool); ok && v {
		q.Set("mine", "true")
	}
	if v, ok := args["overdue_only"].(bool); ok && v {
		q.Set("overdue_only", "true")
	}
	if v := intArg(args, "page"); v > 0 {
		q.Set("page", strconv.Itoa(v))
	}
	if v := intArg(args, "page_size"); v > 0 {
		q.Set("page_size", strconv.Itoa(v))
	}
	data, err := t.client.Do("GET", "/api/v1/tasks?"+q.Encode(), nil)
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
	if id <= 0 {
		return "", fmt.Errorf("task_id 必须为正整数")
	}
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
		Description: "更新任务信息（标题/描述/状态/优先级/进度/截止日期/负责人）。传 clear_assignee 或 clear_due_date 可清空对应字段。权限：编辑基本信息需创建者或管理员；状态/进度流转允许负责人",
		InputSchema: types.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"task_id":        map[string]interface{}{"type": "integer", "description": "任务 ID（必填）"},
				"title":          map[string]interface{}{"type": "string", "description": "新标题"},
				"description":    map[string]interface{}{"type": "string", "description": "新描述"},
				"status":         map[string]interface{}{"type": "string", "enum": []string{"todo", "in_progress", "done"}, "description": "新状态"},
				"priority":       map[string]interface{}{"type": "string", "enum": []string{"low", "medium", "high", "urgent"}, "description": "新优先级"},
				"progress":       map[string]interface{}{"type": "integer", "description": "进度 0-100"},
				"due_date":       map[string]interface{}{"type": "string", "description": "截止时间 YYYY-MM-DD 或 YYYY-MM-DDTHH:MM:SS"},
				"clear_due_date": map[string]interface{}{"type": "boolean", "description": "清空截止日期"},
				"assignee":       map[string]interface{}{"type": "string", "description": "负责人用户名"},
				"clear_assignee": map[string]interface{}{"type": "boolean", "description": "取消负责人指派"},
			},
			Required: []string{"task_id"},
		},
	}
}

func (t *UpdateTask) Call(args map[string]interface{}) (string, error) {
	id := intArg(args, "task_id")
	if id <= 0 {
		return "", fmt.Errorf("task_id 必须为正整数")
	}
	body := map[string]interface{}{}
	setString(body, args, "title", "title")
	setString(body, args, "description", "description")
	setString(body, args, "status", "status")
	setString(body, args, "priority", "priority")
	if v, ok := args["progress"].(float64); ok {
		body["progress"] = int(v)
	}
	if due, err := parseDueDate(args["due_date"]); err != nil {
		return "", err
	} else if due != "" {
		body["due_date"] = due
	}
	if v, ok := args["clear_due_date"].(bool); ok && v {
		body["clear_due_date"] = true
	}
	if name := usernameArg(args); name != "" {
		uid, err := resolveUserID(t.client, name)
		if err != nil {
			return "", err
		}
		body["assignee_id"] = uid
	} else if v, ok := args["clear_assignee"].(bool); ok && v {
		body["clear_assignee"] = true
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
	if id <= 0 {
		return "", fmt.Errorf("task_id 必须为正整数")
	}
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
	if id <= 0 {
		return "", fmt.Errorf("task_id 必须为正整数")
	}
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
		Description: "为任务添加一条进展备注，可同时推进状态与进度",
		InputSchema: types.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"task_id":  map[string]interface{}{"type": "integer", "description": "任务 ID"},
				"comment":  map[string]interface{}{"type": "string", "description": "进展内容（不能为空）"},
				"progress": map[string]interface{}{"type": "integer", "description": "同步设置进度 0-100（可选）"},
				"status":   map[string]interface{}{"type": "string", "enum": []string{"todo", "in_progress", "done"}, "description": "同步变更状态（可选）"},
			},
			Required: []string{"task_id", "comment"},
		},
	}
}

func (t *AddProgress) Call(args map[string]interface{}) (string, error) {
	id := intArg(args, "task_id")
	if id <= 0 {
		return "", fmt.Errorf("task_id 必须为正整数")
	}
	body := map[string]interface{}{}
	setString(body, args, "comment", "comment")
	setString(body, args, "status", "status")
	if v, ok := args["progress"].(float64); ok {
		body["progress"] = int(v)
	}
	if _, ok := body["comment"]; !ok {
		return "", fmt.Errorf("进展内容不能为空")
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
		Description: "查询所有用户列表（后端返回裸数组）",
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
				"page":        map[string]interface{}{"type": "integer", "description": "页码（默认1）"},
				"page_size":   map[string]interface{}{"type": "integer", "description": "每页数量（默认20）"},
			},
		},
	}
}

func (t *ListReminders) Call(args map[string]interface{}) (string, error) {
	q := url.Values{}
	if v, ok := args["unread_only"].(bool); ok && v {
		q.Set("unread_only", "true")
	}
	if v := intArg(args, "page"); v > 0 {
		q.Set("page", strconv.Itoa(v))
	}
	if v := intArg(args, "page_size"); v > 0 {
		q.Set("page_size", strconv.Itoa(v))
	}
	data, err := t.client.Do("GET", "/api/v1/reminders?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	return prettyJSON(data)
}

type CreateReminder struct{ client backendClient }

func (t *CreateReminder) Definition() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "create_reminder",
		Description: "为任务创建一条定时提醒。提醒固定送达任务负责人（无负责人则送创建者），不能指定其他人",
		InputSchema: types.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"task_id":     map[string]interface{}{"type": "integer", "description": "关联任务 ID"},
				"remind_at":   map[string]interface{}{"type": "string", "description": "提醒时间，YYYY-MM-DDTHH:MM:SS 或 YYYY-MM-DD HH:MM:SS"},
				"remind_type": map[string]interface{}{"type": "string", "enum": []string{"manual", "overdue"}, "description": "提醒类型，默认 manual"},
				"message":     map[string]interface{}{"type": "string", "description": "自定义提醒消息（可选）"},
			},
			Required: []string{"task_id", "remind_at"},
		},
	}
}

func (t *CreateReminder) Call(args map[string]interface{}) (string, error) {
	body := map[string]interface{}{}
	setInt(body, args, "task_id", "task_id")
	if intArg(args, "task_id") <= 0 {
		return "", fmt.Errorf("task_id 必须为正整数")
	}
	remindAt, err := parseDueDate(args["remind_at"])
	if err != nil {
		return "", err
	}
	if remindAt == "" {
		return "", fmt.Errorf("remind_at 不能为空")
	}
	body["remind_at"] = remindAt
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
	if id <= 0 {
		return "", fmt.Errorf("reminder_id 必须为正整数")
	}
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

// setQuery 把工具参数透传为查询参数，由 url.Values 负责转义——
// 此前是字符串直接拼接，关键词里带一个 & 就能篡改后面的查询条件。
func setQuery(dst url.Values, src map[string]interface{}, srcKey, dstKey string) {
	if v, ok := src[srcKey].(string); ok && strings.TrimSpace(v) != "" {
		dst.Set(dstKey, v)
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

// usernameArg 取出并修剪用户名参数。
func usernameArg(args map[string]interface{}) string {
	if v, ok := args["assignee"].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// parseDueDate 把日期/时间输入归一化为后端接受的 "YYYY-MM-DDTHH:MM:SS"。
// 只给日期时补零点；带时区偏移的 RFC3339 串原样交给后端解析。
func parseDueDate(v interface{}) (string, error) {
	raw, ok := v.(string)
	if !ok {
		return "", nil
	}
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if len(s) == 10 { // YYYY-MM-DD
		if _, err := time.Parse("2006-01-02", s); err != nil {
			return "", fmt.Errorf("日期格式不合法 %q，应为 YYYY-MM-DD", raw)
		}
		return s + "T00:00:00", nil
	}
	s = strings.Replace(s, " ", "T", 1) // 容忍 "YYYY-MM-DD HH:MM:SS"
	layouts := []string{
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006-01-02T15:04:05Z07:00",
	}
	for _, layout := range layouts {
		if _, err := time.Parse(layout, s); err == nil {
			return s, nil
		}
	}
	return "", fmt.Errorf("时间格式不合法 %q，应为 YYYY-MM-DDTHH:MM:SS", raw)
}

// resolveUserID 把用户名解析为后端用户 ID。
//
// 两个修正：① /api/v1/users 返回的是裸数组，旧代码按 {"data": [...]} 解析，
// 结果永远为空，指派负责人会静默失效；② 找不到用户时必须报错，
// 而不是把“指派给某人”变成“创建一条没有负责人的任务”。
func resolveUserID(client backendClient, username string) (uint, error) {
	data, err := client.Do("GET", "/api/v1/users", nil)
	if err != nil {
		return 0, fmt.Errorf("查询用户列表失败: %w", err)
	}
	var users []struct {
		ID       uint   `json:"id"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal(data, &users); err != nil {
		return 0, fmt.Errorf("解析用户列表失败: %w", err)
	}
	var candidates []string
	for _, u := range users {
		if u.Username == username {
			return u.ID, nil
		}
		if strings.EqualFold(u.Username, username) {
			candidates = append(candidates, u.Username)
		}
	}
	// 后端用户名区分大小写，这里允许唯一的不区分大小写匹配
	if len(candidates) == 1 {
		for _, u := range users {
			if u.Username == candidates[0] {
				return u.ID, nil
			}
		}
	}
	if len(candidates) > 1 {
		return 0, fmt.Errorf("用户名 %q 匹配到多个账号：%s，请给出确切拼写", username, strings.Join(candidates, ", "))
	}
	return 0, fmt.Errorf("找不到用户 %q，先用 list_users 确认用户名", username)
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
