package onelink

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	sdk "github.com/onelink/platform/sdk/go/onelink"

	"taskbackend/internal/model"
)

// Profiles 本地用户档案(user 表)。
//
// 它是**档案**而不是身份: 账号、口令、组织、角色授权全在 OneLink。这张表存在的唯一
// 理由是 7 张业务表的外键都指向 user.id, 而改成平台的用户主键意味着改所有关联列、
// 迁移存量数据、改所有 join —— 换来的只是"少一张表"。
//
// 因此它的纪律只有一条: **业务外键用 user.id(本地主键), 映射用 user.onelink_uid**。
// 不要拿 username 去关联任何业务数据 —— 账号在平台侧是可以被改的, 改完之后历史数据
// 会挂到一个"同名的新人"上。
type Profiles struct{ db *gorm.DB }

// NewProfiles 构造。
func NewProfiles(db *gorm.DB) *Profiles { return &Profiles{db: db} }

// Upsert 按平台用户主键建/更新本地档案, 返回本地那一行。
//
// 存量数据不做对账(需求如此: 存量用户不予考虑), 所以第一次从门户进来的人在这里自动
// 建档。已建档的人每次进来同步一次平台侧的权威资料(姓名/头像/账号可能在平台上改了)。
func (p *Profiles) Upsert(ctx context.Context, id sdk.Identity) (*model.User, error) {
	if id.UserID <= 0 {
		return nil, errors.New("onelink: 平台用户主键缺失, 无法建立本地档案")
	}
	uid := id.UserID

	var u model.User
	err := p.db.WithContext(ctx).Where("onelink_uid = ?", uid).First(&u).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return p.create(ctx, uid, id)
	case err != nil:
		return nil, err
	}

	// 已建档: 同步平台说了算的那几列。
	//
	// IsActive 不从平台推: 平台的停用会通过会话失效体现(单点登出 + 会话存活轮询),
	// 而本地这一列是"这个人还能不能被指派任务"的业务开关, 两件事不该混。
	updates := map[string]any{
		"full_name":  firstNonEmpty(id.RealName, id.NickName, u.FullName),
		"avatar_url": avatarOrKeep(id.Avatar, u.AvatarURL),
	}
	if id.Username != "" && id.Username != u.Username {
		// 平台侧改名。本地 username 有唯一索引, 所以改之前必须确认新名字没被别人占着 ——
		// 撞上时保留本地值并继续, 而不是让这个人因为"平台那边改了名"而登不进来。
		var taken int64
		if err := p.db.WithContext(ctx).Model(&model.User{}).
			Where("username = ? AND onelink_uid <> ?", id.Username, uid).
			Count(&taken).Error; err != nil {
			return nil, err
		}
		if taken == 0 {
			updates["username"] = id.Username
		}
	}
	if err := p.db.WithContext(ctx).Model(&model.User{}).Where("id = ?", u.ID).
		Updates(updates).Error; err != nil {
		return nil, err
	}
	if err := p.db.WithContext(ctx).First(&u, u.ID).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// create 首次建档。
//
// username/email 上有唯一索引, 而它们现在都由平台给出。撞上时**报错而不是改一个名字
// 蒙过去**: 唯一索引在这里真正挡住的是"库里还留着一个平台侧已经不存在的老账号",
// 那是运维该去看一眼的事(清掉那行, 或把它的 onelink_uid 补上), 而不是应用该悄悄绕开的事
// —— 悄悄绕开的表现是"同一个人的任务被分到了两个账号上"。
func (p *Profiles) create(ctx context.Context, uid int64, id sdk.Identity) (*model.User, error) {
	u := &model.User{
		OnelinkUID: &uid,
		Username:   firstNonEmpty(id.Username, fmt.Sprintf("onelink-%d", uid)),
		Email:      strings.ToLower(emailFor(id, uid)),
		FullName:   firstNonEmpty(id.RealName, id.NickName, id.Username),
		IsActive:   true,
	}
	if id.Avatar != "" {
		avatar := id.Avatar
		u.AvatarURL = &avatar
	}
	if err := p.db.WithContext(ctx).Create(u).Error; err != nil {
		return nil, fmt.Errorf(
			"建立本地档案失败(平台用户 %d, 账号 %q): %w —— "+
				"多半是库里还留着一行同名/同邮箱的旧账号, 请先处置它(清掉, 或把它的 onelink_uid 补成 %d)",
			uid, u.Username, err, uid)
	}
	return u, nil
}

// emailFor 平台没给邮箱时的兜底。
//
// 本地 email 有唯一索引且非空, 而 OneLink 的 openid 作用域**不含**邮箱 —— 于是"没给"
// 是常态而不是异常。用一个从用户主键推出来的占位地址, 是为了让"这个人的邮箱我们不知道"
// 有一个确定的表示; 拿空串去填会让第二个没有邮箱的人在唯一索引上撞车。
func emailFor(id sdk.Identity, uid int64) string {
	if e := strings.TrimSpace(id.Username); strings.Contains(e, "@") {
		return e
	}
	return fmt.Sprintf("onelink-%d@unknown.local", uid)
}

// avatarOrKeep 平台给了头像就用平台的, 没给就保留本地已有的。
//
// 不回退成空指针: 头像为空时把本地那一列清掉, 表现是"用户换了个没配头像的账号之后
// 自己上传的头像没了", 而这件事没有任何人会觉得是设计如此。
func avatarOrKeep(fromPlatform string, current *string) any {
	if fromPlatform != "" {
		return fromPlatform
	}
	if current == nil {
		return nil
	}
	return *current
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
