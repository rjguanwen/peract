package model

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"golang.org/x/crypto/bcrypt"
)

// User 用户
type User struct {
	ID               uint    `gorm:"primaryKey" json:"id"`
	Username         string  `gorm:"size:64;uniqueIndex" json:"username"`
	Email            string  `gorm:"size:255;uniqueIndex" json:"email"`
	FullName         string  `gorm:"size:128" json:"full_name"`
	HashedPassword   string  `gorm:"size:255" json:"-"`
	Role             string  `gorm:"size:16;default:user" json:"role"`
	IsActive         bool    `gorm:"default:true" json:"is_active"`
	AvatarURL        *string `gorm:"size:255" json:"avatar_url"` // 头像（/uploads/avatars/xxx.png）
	Signature        string  `gorm:"size:255" json:"signature"`  // 个性签名
	PasswordHint     string  `gorm:"size:128" json:"-"`          // 密码提示词
	SecurityQuestion string  `gorm:"size:128" json:"-"`          // 找回安全问题
	// SecurityAnswer 存答案的 bcrypt 哈希（写入前统一 TrimSpace + 小写）。
	// 历史数据可能是明文，校验时经 IsLegacySecurityAnswer 识别并要求用户重设。
	SecurityAnswer string `gorm:"size:255" json:"-"`
	// PasswordChangedAt 是口令版本号：早于该时刻签发的令牌一律失效，
	// 否则重置密码拦不住已经窃用到手的会话。NULL 表示改造前的存量账号，不做回溯。
	PasswordChangedAt *time.Time `gorm:"column:password_changed_at" json:"-"`
	CreatedAt         time.Time  `json:"created_at"`
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

// normalizeSecurityAnswer 统一答案书写差异（大小写、首尾空白、全角空格）。
func normalizeSecurityAnswer(answer string) string {
	return strings.ToLower(strings.TrimSpace(strings.ReplaceAll(answer, "\u3000", " ")))
}

// SetSecurityAnswer 哈希安全答案；空答案表示清除。
func (u *User) SetSecurityAnswer(answer string) error {
	normalized := normalizeSecurityAnswer(answer)
	if normalized == "" {
		u.SecurityAnswer = ""
		return nil
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(normalized), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	u.SecurityAnswer = string(hashed)
	return nil
}

// CheckSecurityAnswer 校验安全答案。哈希值来自数据库、答案由用户提供，
// bcrypt 比对本身不泄露答案内容，故无需额外常数时间处理。
func (u *User) CheckSecurityAnswer(answer string) bool {
	normalized := normalizeSecurityAnswer(answer)
	if normalized == "" || u.SecurityAnswer == "" {
		return false
	}
	if u.IsLegacySecurityAnswer() {
		// 旧明文数据不允许通过校验，避免明文被当作有效凭证使用
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(u.SecurityAnswer), []byte(normalized)) == nil
}

// IsLegacySecurityAnswer 判断存量答案是否为改造前写入的明文（非 bcrypt 哈希）。
func (u *User) IsLegacySecurityAnswer() bool {
	return u.SecurityAnswer != "" && !strings.HasPrefix(u.SecurityAnswer, "$2")
}

// ErrSecurityAnswerLegacy 存量明文答案，需用户重新设置后才能继续找回密码。
var ErrSecurityAnswerLegacy = errors.New("security answer stored in plaintext")

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

// TaskShare 任务分享记录（创建者可将自己的任务分享给其他用户查看）
type TaskShare struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	TaskID     uint      `gorm:"index;not null" json:"task_id"`
	UserID     uint      `gorm:"index;not null" json:"user_id"` // 被分享者
	GrantedBy  uint      `gorm:"not null" json:"granted_by"`     // 分享人（必须是任务创建者）
	CreatedAt  time.Time `json:"created_at"`
}

// TaskShareUnique 同一任务同一用户不允许重复分享（联合唯一约束）
func (TaskShare) TableName() string { return "task_shares" }

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
