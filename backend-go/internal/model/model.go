package model

import (
	"time"

	"gorm.io/gorm"

	"golang.org/x/crypto/bcrypt"
)

// User 用户
type User struct {
	ID               uint       `gorm:"primaryKey" json:"id"`
	Username         string     `gorm:"size:64;uniqueIndex" json:"username"`
	Email            string     `gorm:"size:255;uniqueIndex" json:"email"`
	FullName         string     `gorm:"size:128" json:"full_name"`
	HashedPassword   string     `gorm:"size:255" json:"-"`
	Role             string     `gorm:"size:16;default:user" json:"role"`
	IsActive         bool       `gorm:"default:true" json:"is_active"`
	AvatarURL        *string    `gorm:"size:255" json:"avatar_url"` // 头像（/uploads/avatars/xxx.png）
	Signature        string     `gorm:"size:255" json:"signature"`  // 个性签名
	PasswordHint     string     `gorm:"size:128" json:"-"`          // 密码提示词
	SecurityQuestion string     `gorm:"size:128" json:"-"`          // 找回安全问题
	SecurityAnswer   string     `gorm:"size:128" json:"-"`          // 安全答案（存小写，不回传）
	CreatedAt        time.Time  `json:"created_at"`
}

func NewUser(username, email, password, role, fullName string) *User {
	hashed, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return &User{
		Username:       username,
		Email:          email,
		FullName:       fullName,
		HashedPassword: string(hashed),
		Role:           role,
		IsActive:       true,
	}
}

func (u *User) CheckPassword(password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(u.HashedPassword), []byte(password)) == nil
}

func (u *User) SetPassword(password string) error {
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	u.HashedPassword = string(hashed)
	return nil
}

// Task 任务
type Task struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	Title       string         `gorm:"size:255" json:"title"`
	Description string         `gorm:"type:text" json:"description"`
	Status      string         `gorm:"size:32;index;default:todo" json:"status"`
	Priority    string         `gorm:"size:16;index;default:medium" json:"priority"`
	AssigneeID  *uint          `gorm:"index" json:"assignee_id"`
	CreatorID   *uint          `json:"creator_id"`
	DueDate     *DateTime      `gorm:"index" json:"due_date"`
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
	TaskID    uint      `gorm:"index" json:"task_id"`
	UserID    *uint     `gorm:"index" json:"user_id"`
	Action    string    `gorm:"size:32;default:comment" json:"action"`
	Comment   string    `gorm:"type:text" json:"comment"`
	OldStatus *string   `gorm:"size:32" json:"old_status"`
	NewStatus *string   `gorm:"size:32" json:"new_status"`
	Progress  *int      `json:"progress"`
	CreatedAt time.Time `json:"created_at"`

	UserName string `gorm:"-" json:"user_name"`
	User     *User  `gorm:"foreignKey:UserID" json:"-"`
}

// Reminder 提醒/通知
type Reminder struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	TaskID     uint       `gorm:"index" json:"task_id"`
	UserID     *uint      `gorm:"index" json:"user_id"`
	RemindAt   time.Time  `gorm:"index" json:"remind_at"`
	RemindType string     `gorm:"size:32;default:manual" json:"remind_type"`
	Message    string     `gorm:"type:text" json:"message"`
	Sent       bool       `json:"sent"`
	SentAt     *time.Time `json:"sent_at"`
	ReadAt     *time.Time `json:"read_at"`
	CreatedAt  time.Time  `json:"created_at"`

	TaskTitle string `gorm:"-" json:"task_title"`
	Task      *Task  `gorm:"foreignKey:TaskID" json:"-"`
}

// SystemSetting 系统设置（key-value，与用户无关）
type SystemSetting struct {
	Key   string `gorm:"primaryKey;size:64" json:"key"`
	Value string `gorm:"size:255" json:"value"`
}

// 系统设置键
const (
	SettingRegistrationEnabled = "registration_enabled" // "true"/"false"
)

// Invitation 邀请注册记录（管理员邀请指定邮箱注册）
type Invitation struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	Email     string     `gorm:"size:255;uniqueIndex;not null" json:"email"`
	Token     string     `gorm:"size:64;uniqueIndex;not null" json:"-"` // 随机邀请令牌（仅存库，不回传）
	InvitedBy uint       `gorm:"not null" json:"invited_by"`            // 发起邀请的管理员 id
	Status    string     `gorm:"size:16;default:pending" json:"status"` // pending / registered / revoked
	ExpiresAt time.Time  `gorm:"not null" json:"expires_at"`
	CreatedAt time.Time  `gorm:"not null" json:"created_at"`
	UsedAt    *time.Time `json:"used_at"` // 实际完成注册的时间
}

// 邀请状态
const (
	InviteStatusPending    = "pending"    // 待接受
	InviteStatusRegistered = "registered" // 已注册（链接已被使用）
	InviteStatusRevoked    = "revoked"    // 已撤销
)

// 常量
const (
	StatusTodo       = "todo"
	StatusInProgress = "in_progress"
	StatusDone       = "done"

	PriorityLow    = "low"
	PriorityMedium = "medium"
	PriorityHigh   = "high"
	PriorityUrgent = "urgent"

	RoleUser  = "user"
	RoleAdmin = "admin"

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
