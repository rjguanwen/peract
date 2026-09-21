// Package onelink 是躬行接入 OneLink 的适配层。
//
// 它只做三件事, 而这三件事都不该散进业务代码:
//
//	store.go   把平台会话放进躬行自己的 SQLite
//	guard.go   把 SDK 的 net/http 守卫桥接成 gin 中间件
//	profile.go 首次从门户进来时按 onelink_uid 建本地档案
//
// 身份与治理层(账号、口令、组织、菜单、角色授权)由 OneLink 接管; 躬行只留业务数据
// (任务、进展、提醒、分享)与一份**本地用户档案**。保留档案表而不是把平台的用户主键
// 直接当业务外键, 是因为 7 张业务表的外键都指向 user.id —— 改它们的代价比加一个映射列
// 高一个数量级, 而业务语义没变: 任务还是挂在"某个用户"上, 只是这个用户的身份来源变了。
package onelink

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"gorm.io/gorm"

	sdk "github.com/onelink/platform/sdk/go/onelink"
)

// SessionRow 一条应用会话在躬行 SQLite 里的行。
//
// 为什么用躬行自己的库而不是 SDK 的 MemoryStore: 后者在进程重启后把所有人的会话一起
// 丢掉, 表现是"服务发布一次, 全体被踢回门户"。而躬行本来就是单副本 SQLite 架构
// (见 README 的技术选型), 用 SQLite 存会话与它自洽 —— SDK 的 MemoryStore 注释要求
// "多副本必须换共享实现", 这里等于把"接受单副本"这个既有事实写进会话层。
//
// LocalID 是 SDK 自己生成的随机主键(cookie 里就是它), **不是**平台的 sid:
// 平台 sid 只在这张表里, 不进 cookie、不进 URL —— 它是平台 Redis 的键名, 出现在
// 浏览器或代理日志里等于把"定位一次登录"的坐标交给了链路中间的每一跳。
//
// Permissions/Scopes 存 JSON 文本而不是逗号分隔: 权限码的字符集里本来就有 ':' 与 '-',
// 再用一个分隔符去拼就迟早会撞上 —— 而撞上的表现是"某个权限点时有时无"。
type SessionRow struct {
	LocalID        string `gorm:"primaryKey;size:64"`
	PlatformSID    string `gorm:"size:64;index"`
	UserID         int64  `gorm:"index"`
	Username       string `gorm:"size:64"`
	NickName       string `gorm:"size:128"`
	RealName       string `gorm:"size:128"`
	Avatar         string `gorm:"size:255"`
	OrgID          int64
	UserType       int8
	SuperAdmin     bool
	MustChangePass bool
	Permissions    string `gorm:"type:text"`
	Scopes         string `gorm:"type:text"`
	AccessToken    string `gorm:"type:text"`
	RefreshToken   string `gorm:"type:text"`
	AccessExpires  time.Time
	RefreshExpires time.Time
	LastAliveCheck time.Time
	CreatedAt      time.Time
}

// TableName 表名。显式给出来而不是靠 GORM 的复数推导: 推导出来的名字会随结构体改名而变,
// 而"改一个结构体名字导致线上会话表被新建一张空表"是一次所有人都被踢下线的故障。
func (SessionRow) TableName() string { return "onelink_sessions" }

// Store 用躬行的 SQLite 实现 sdk.Store(那四个方法)。
type Store struct {
	db *gorm.DB

	// mu 串行化 Update 的读-改-写。
	//
	// 这不是性能优化, 是正确性。SQLite 的写锁是库级的, 但它是在**第一条写语句**上才拿的:
	// 两个并发的 Update 可以各自读到同一份旧记录, 然后依次写回 —— 后写的那份把先写的那份
	// 整个覆盖掉。而被覆盖的字段里包含**轮换中的刷新令牌**: 覆盖回去之后, 下一次续期会拿
	// 一个已经用过的旧令牌去换, 撞上平台的 20006(刷新令牌重放)。那个错误码在平台侧是被
	// 当成"凭据疑似泄露"处理的告警, 而现场看起来就像有人在盗用。
	//
	// 进程级互斥在这里是够的, 因为躬行是单副本部署(上面 SessionRow 的注释已经把这笔账
	// 算过了)。哪天要多副本, 换的不是这把锁, 而是整个 Store。
	mu sync.Mutex
}

// NewStore 构造。
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// Migrate 建表。
//
// 与 database.Migrate 分开是有意的: 这张表的列跟着 SDK 的 SessionState 走, 而
// database.Migrate 管的是业务表。放在同一个函数里会让"升级 SDK 时记得改迁移"变成
// 一件没有人记得的事 —— 而漏掉它的表现是启动时 AutoMigrate 顺手加列(看起来没事),
// 或者是运行期某几个字段永远是零值。
func (s *Store) Migrate() error { return s.db.AutoMigrate(&SessionRow{}) }

// Load 见 sdk.Store。
//
// 不存在时回 (nil, nil): "没有会话"是正常分支(用户第一次从门户进来), 不是一个需要
// 调用方去匹配错误字符串的事实。
func (s *Store) Load(ctx context.Context, id string) (*sdk.SessionState, error) {
	if id == "" {
		return nil, nil
	}
	var row SessionRow
	err := s.db.WithContext(ctx).Where("local_id = ?", id).First(&row).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}
	return row.toState()
}

// Update 见 sdk.Store。读-改-写在一把进程级锁内完成(理由见 Store.mu)。
func (s *Store) Update(ctx context.Context, id string, fn func(*sdk.SessionState) error) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var row SessionRow
	err := s.db.WithContext(ctx).Where("local_id = ?", id).First(&row).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		// 接口约定: 记录不存在时 fn 不被调用且返回 (false, nil)。
		return false, nil
	case err != nil:
		return false, err
	}
	state, err := row.toState()
	if err != nil {
		return false, err
	}
	if err := fn(state); err != nil {
		// fn 返回 error 时不落盘。它拿到的是从 row 解出来的**副本**, 所以"改了一半
		// 又返回错误"不会污染任何已落盘的东西。
		return false, err
	}
	next := fromState(state)
	// 只写会话自身会变的列, 不整行覆盖: LocalID/CreatedAt 是创建时定死的,
	// 把它们一起写回等于给"某次 Update 把创建时间改成了现在"留一条路。
	err = s.db.WithContext(ctx).Model(&SessionRow{}).Where("local_id = ?", id).
		Updates(map[string]any{
			"platform_sid":     next.PlatformSID,
			"user_id":          next.UserID,
			"username":         next.Username,
			"nick_name":        next.NickName,
			"real_name":        next.RealName,
			"avatar":           next.Avatar,
			"org_id":           next.OrgID,
			"user_type":        next.UserType,
			"super_admin":      next.SuperAdmin,
			"must_change_pass": next.MustChangePass,
			"permissions":      next.Permissions,
			"scopes":           next.Scopes,
			"access_token":     next.AccessToken,
			"refresh_token":    next.RefreshToken,
			"access_expires":   next.AccessExpires,
			"refresh_expires":  next.RefreshExpires,
			"last_alive_check": next.LastAliveCheck,
		}).Error
	if err != nil {
		return false, err
	}
	return true, nil
}

// Put 见 sdk.Store。同一条 LocalID 重复写入按覆盖处理: SDK 只在新建时调它,
// 而"已经存在就报错"会让一次重放(网关重发、客户端重试)变成登录失败。
func (s *Store) Put(ctx context.Context, st *sdk.SessionState) error {
	if st == nil || st.LocalID == "" {
		return errors.New("onelink: 会话缺少 LocalID")
	}
	row := fromState(st)
	return s.db.WithContext(ctx).
		Where("local_id = ?", row.LocalID).
		Assign(row).
		FirstOrCreate(&SessionRow{}).Error
}

// Delete 见 sdk.Store。不存在不算错(登出时无论有没有都清一遍)。
func (s *Store) Delete(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Where("local_id = ?", id).Delete(&SessionRow{}).Error
}

// PurgeExpired 删掉彻底过期的会话行。
//
// 与 SDK 的 MemoryStore 不同, 数据库里的行不会自己消失。不清的话这张表只增不减,
// 而它每一行都带着两个令牌原文 —— 那是"一个已经没人用的库里躺着几百张可用凭据"。
// 判据取刷新令牌的到期时刻: 访问令牌过期不销毁会话, 那正是续期存在的意义。
func (s *Store) PurgeExpired(ctx context.Context, now time.Time) (int64, error) {
	res := s.db.WithContext(ctx).
		Where("refresh_expires < ?", now).
		Delete(&SessionRow{})
	return res.RowsAffected, res.Error
}

// toState 行 -> SDK 的会话状态。
func (r *SessionRow) toState() (*sdk.SessionState, error) {
	perms, err := decodeStrings(r.Permissions)
	if err != nil {
		return nil, err
	}
	scopes, err := decodeStrings(r.Scopes)
	if err != nil {
		return nil, err
	}
	return &sdk.SessionState{
		LocalID: r.LocalID,
		Identity: sdk.Identity{
			SessionID: r.PlatformSID, UserID: r.UserID,
			Username: r.Username, NickName: r.NickName, RealName: r.RealName,
			Avatar: r.Avatar, OrgID: r.OrgID, UserType: r.UserType,
			SuperAdmin: r.SuperAdmin, Permissions: perms, Scopes: scopes,
			MustChangePassword: r.MustChangePass,
		},
		AccessToken:      r.AccessToken,
		RefreshToken:     r.RefreshToken,
		AccessExpiresAt:  r.AccessExpires,
		RefreshExpiresAt: r.RefreshExpires,
		CreatedAt:        r.CreatedAt,
		LastAliveCheck:   r.LastAliveCheck,
	}, nil
}

// fromState 会话状态 -> 行。
func fromState(st *sdk.SessionState) *SessionRow {
	return &SessionRow{
		LocalID:        st.LocalID,
		PlatformSID:    st.SessionID,
		UserID:         st.UserID,
		Username:       st.Username,
		NickName:       st.NickName,
		RealName:       st.RealName,
		Avatar:         st.Avatar,
		OrgID:          st.OrgID,
		UserType:       st.UserType,
		SuperAdmin:     st.SuperAdmin,
		MustChangePass: st.MustChangePassword,
		Permissions:    encodeStrings(st.Permissions),
		Scopes:         encodeStrings(st.Scopes),
		AccessToken:    st.AccessToken,
		RefreshToken:   st.RefreshToken,
		AccessExpires:  st.AccessExpiresAt,
		RefreshExpires: st.RefreshExpiresAt,
		LastAliveCheck: st.LastAliveCheck,
		CreatedAt:      st.CreatedAt,
	}
}

// encodeStrings 空切片编码成 "[]" 而不是空串。
//
// 空串在解回来时会被 decodeStrings 判成"这个字段从来没写过"并回 nil —— 于是"权限被
// 收干净了"与"字段丢了"长得一模一样, 而前者会让界面上的按钮全部消失, 后者不会。
func encodeStrings(in []string) string {
	if in == nil {
		in = []string{}
	}
	b, err := json.Marshal(in)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func decodeStrings(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}
