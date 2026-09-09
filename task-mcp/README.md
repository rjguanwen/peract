# 躬行（Peract）· Task MCP Server

通过 [Model Context Protocol (MCP)](https://modelcontextprotocol.io) 暴露躬行（任务管理）的全部功能，AI agent 可直接调用工具操作任务。

## 快速开始

```bash
cd task-mcp
go build -o task-mcp.exe ./cmd/server

# 运行（默认连接 localhost:8001，admin/admin123）
./task-mcp.exe

# 自定义后端地址和凭证
BACKEND_URL=http://localhost:8001 MCP_USERNAME=admin MCP_PASSWORD=admin123 ./task-mcp.exe
```

## 可用工具（共 12 个）

| 工具 | 说明 |
|------|------|
| `create_task` | 创建任务（标题/描述/优先级/截止日期/负责人） |
| `list_tasks` | 查询任务列表（状态/优先级/负责人/分页筛选） |
| `get_task` | 获取单个任务详情 |
| `update_task` | 更新任务信息（支持状态流转、进度更新） |
| `delete_task` | 软删除任务（放入回收站） |
| `restore_task` | 从回收站恢复任务 |
| `list_deleted_tasks` | 查看回收站 |
| `add_progress` | 添加任务进展记录 |
| `list_users` | 查询所有用户 |
| `get_me` | 获取当前登录用户信息 |
| `list_reminders` | 查看提醒列表（支持未读筛选） |
| `create_reminder` | 创建任务提醒 |
| `mark_reminder_read` | 标记提醒已读 |
| `get_stats` | 获取仪表盘统计数据 |

## MCP 协议

- 传输方式：stdio（标准输入/输出 JSON-RPC 2.0）
- 协议版本：2024-11-05
- 通信格式：每行一个 JSON-RPC 消息（以 `\n` 分隔）

## 与 AI Agent 集成

在 CodeBuddy 中配置 MCP server 路径后，AI agent 可直接调用上述工具，无需手动调用 REST API。
