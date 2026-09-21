package model

import (
	"time"

	"gorm.io/gorm"
)

// User 用户。
//
// 这张表在接入 OneLink 之后**降级为本地用户档案**: 账号、口令、组织、角色授权都由平台
// 接管, 这里留下的是一份映射(OnelinkUID)与业务上需要的展示字段。
//
// 为什么不把平台的用户主键直接当业务外键: 7 张业务表(Task/TaskProgress/Reminder/
// TaskShare/Invitation...)的外键都指向 user.id。改成平台主键意味着改所有关联列、
// 迁移存量数据、改所有 join —— 换来的只是"少一张表", 而业务语义完全没变。
//
// 纪律只有一条: **业务外键用 id(本地主键), 映射用 onelink_uid**。不要拿 username
// 关联业务数据 —— 账号在平台侧是可以被改的, 改完之后历史数据会挂到"同名的新人"上。
type User struct {
	ID uint `gorm:"primaryKey" json:"id"`
	// OnelinkUID 平台用户主键。用指针是因为存量行在映射之前是 NULL,
	// 而 NULL 与 0 在"这个人从哪来"这个问题上是两件事(0 不是任何人的主键)。
	OnelinkUID *int64 `gorm:"uniqueIndex" json:"onelink_uid"`
	Username   string `gorm:"size:64;uniqueIndex" json:"username"`
	Email      string `gorm:"size:255;uniqueIndex" json:"email"`
	FullName   string `gorm:"size:128" json:"full_name"`
	// IsActive 是**业务开关**: "这个人还能不能被指派任务", 与平台的账号启用状态无关。
	// 平台停用一个人会通过会话失效体现(单点登出 + 存活轮询), 那件事不该混进这一列。
	IsActive  bool      `gorm:"default:true" json:"is_active"`
	AvatarURL *string   `gorm:"size:255" json:"avatar_url"` // 头像（/uploads/avatars/xxx.png）
	Signature string    `gorm:"size:255" json:"signature"`  // 个性签名
	CreatedAt time.Time `json:"created_at"`
}

// 注意: hashed_password / role / password_hint / security_question / security_answer /
// password_changed_at 这几列**在库里还在**, 只是结构体不再映射它们。
//
// GORM 的 AutoMigrate 从不删列, 所以它们在存量库上会一直留着 —— 那正是我们要的:
// 一次"顺手清理"会让旧数据不可逆地消失, 而它们留在那里不影响任何逻辑。
// 确认不再需要回溯时, 手工 DROP 即可:
//
//	ALTER TABLE users DROP COLUMN hashed_password;  -- 以及其余五列
//
// 为什么要删字段而不是留着: 留着就等于留着一个"应用还能自己管口令"的入口,
// 而那种入口一旦被人顺手用起来, 平台的账号体系就多了一个不受它管的分支。

// NewUser 建一条**本地档案**。
//
// 参数里没有口令与角色, 而且这不是签名简化: 接入 OneLink 之后, 应用侧不再有任何一处
// 需要构造口令哈希或角色字符串。留着那两个参数会诱使人再写出一个"应用自己管账号"的
// 调用点, 而那种调用点一旦出现, 平台的账号体系就多了一个不受它管的分支。
//
// OnelinkUID 由调用方填(internal/onelink 的建档逻辑), 因为只有它手里有平台用户主键。
func NewUser(username, email, fullName string) *User {
	return &User{
		Username: username,
		Email:    email,
		FullName: fullName,
		IsActive: true,
	}
}

// Task 任务
type Task struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	Title       string         `gorm:"size:255" json:"title"`
	Description string         `gorm:"type:text" json:"description"`
	Status      string         `gorm:"size:32;index;default:todo;index:idx_task_assignee_status,priority:2;index:idx_task_due_status,priority:2" json:"status"`
	Priority    string         `gorm:"size:16;index;default:medium" json:"priority"`
	AssigneeID  *uint          `gorm:"index;index:idx_task_assignee_status,priority:1" json:"assignee_id"`
	CreatorID   *uint          `gorm:"index:idx_task_creator" json:"creator_id"`
	DueDate     *DateTime      `gorm:"index;index:idx_task_due_status,priority:1" json:"due_date"`
	Progress    int            `json:"progress"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"deleted_at"` // 逻辑删除

	// 计算/展示字段（不落库）
	IsOverdue  bool           `gorm:"-" json:"is_overdue"`
	Assignee   *User          `gorm:"foreignKey:AssigneeID" json:"-"`
	Creator    *User          `gorm:"foreignKey:CreatorID" json:"-"`
	Progresses []TaskProgress `gorm:"foreignKey:TaskID" json:"-"`
}

// IsDeleted 是否已逻辑删除
func (t *Task) IsDeleted() bool {
	return t.DeletedAt.Valid
}

func (t *Task) ComputeOverdue() {
	t.IsOverdue = t.DueDate != nil && t.Status != "done" && t.DueDate.Before(time.Now())
}

// TaskProgress 任务进展记录
type TaskProgress struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	TaskID    uint      `gorm:"index;index:idx_progress_task_created,priority:1" json:"task_id"`
	UserID    *uint     `gorm:"index" json:"user_id"`
	Action    string    `gorm:"size:32;default:comment" json:"action"`
	Comment   string    `gorm:"type:text" json:"comment"`
	OldStatus *string   `gorm:"size:32" json:"old_status"`
	NewStatus *string   `gorm:"size:32" json:"new_status"`
	Progress  *int      `json:"progress"`
	CreatedAt time.Time `gorm:"index:idx_progress_task_created,priority:2" json:"created_at"`

	UserName string `gorm:"-" json:"user_name"`
	User     *User  `gorm:"foreignKey:UserID" json:"-"`
}

// Reminder 提醒/通知
type Reminder struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	TaskID     uint       `gorm:"index" json:"task_id"`
	UserID     *uint      `gorm:"index;index:idx_reminder_user_unread,priority:1" json:"user_id"`
	RemindAt   time.Time  `gorm:"index;index:idx_reminder_sent_due,priority:1" json:"remind_at"`
	RemindType string     `gorm:"size:32;default:manual;index:idx_reminder_type_created,priority:1" json:"remind_type"`
	Message    string     `gorm:"type:text" json:"message"`
	Sent       bool       `gorm:"index:idx_reminder_sent_due,priority:2" json:"sent"`
	SentAt     *time.Time `json:"sent_at"`
	ReadAt     *time.Time `gorm:"index:idx_reminder_user_unread,priority:2" json:"read_at"`
	CreatedAt  time.Time  `gorm:"index:idx_reminder_type_created,priority:2" json:"created_at"`

	TaskTitle string `gorm:"-" json:"task_title"`
	Task      *Task  `gorm:"foreignKey:TaskID" json:"-"`
}

// SystemSetting 系统设置（key-value，与用户无关）
type SystemSetting struct {
	Key   string `gorm:"primaryKey;size:64" json:"key"`
	Value string `gorm:"size:255" json:"value"`
}

// TaskShare 任务分享记录（创建者可将自己的任务分享给其他用户查看）
type TaskShare struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	TaskID    uint      `gorm:"index;not null" json:"task_id"`
	UserID    uint      `gorm:"index;not null" json:"user_id"` // 被分享者
	GrantedBy uint      `gorm:"not null" json:"granted_by"`    // 分享人（必须是任务创建者）
	CreatedAt time.Time `json:"created_at"`
}

// TaskShareUnique 同一任务同一用户不允许重复分享（联合唯一约束）
func (TaskShare) TableName() string { return "task_shares" }

// 常量
//
// 这里**没有** RoleUser/RoleAdmin: 角色的载体是平台侧的角色授权
// (sys_user_role.app_id 决定"他在哪个应用下持有哪个角色"), 应用侧拿到的只有权限码
// 快照。用一个字符串字段表达角色, 就等于在应用侧再造一份会漂走的真值。
const (
	StatusTodo       = "todo"
	StatusInProgress = "in_progress"
	StatusDone       = "done"

	PriorityLow    = "low"
	PriorityMedium = "medium"
	PriorityHigh   = "high"
	PriorityUrgent = "urgent"

	ActionCreated        = "created"
	ActionAssigned       = "assigned"
	ActionStatusChanged  = "status_changed"
	ActionProgressUpdate = "progress_updated"
	ActionComment        = "comment"

	RemindManual  = "manual"
	RemindOverdue = "overdue"
	RemindAssign  = "assign"
	RemindStatus  = "status"
)
