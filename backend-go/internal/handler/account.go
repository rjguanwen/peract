package handler

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"taskbackend/internal/middleware"
	"taskbackend/internal/model"
	"taskbackend/internal/service"
)

// 找回密码邮件正文里的有效期提示，需与 middleware.SignResetToken 的 30 分钟保持一致
const resetLinkTTLHint = "30 分钟"

// getSetting / setSetting 读写系统设置
func (h *Handler) getSetting(key, def string) string {
	var s model.SystemSetting
	if err := h.db.Where("key = ?", key).First(&s).Error; err != nil {
		return def
	}
	return s.Value
}

// setSetting 用 ON CONFLICT 做单语句 upsert。
// 原先走 db.Save，缺失记录时会先 UPDATE 再 INSERT，两条语句之间存在并发插入窗口，
// 命中主键冲突后直接报错；upsert 没有这个问题。
func (h *Handler) setSetting(key, value string) error {
	return h.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&model.SystemSetting{Key: key, Value: value}).Error
}

// ChangePassword PUT /auth/password 修改自己的密码
func (h *Handler) ChangePassword(c *gin.Context) {
	ctx := currentUser(c)
	var req struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "参数有误")
		return
	}
	if req.OldPassword == "" || req.NewPassword == "" {
		badRequest(c, "请填写原密码与新密码")
		return
	}
	if len(req.NewPassword) < 6 {
		badRequest(c, "新密码至少需要 6 个字符")
		return
	}
	// bcrypt 校验本身就昂贵，已登录会话也不能无限次触发
	if !throttle(c, h.loginLimiter, "chpw:"+ctx.Username) {
		return
	}
	var user model.User
	if err := h.db.First(&user, ctx.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			notFound(c, "用户不存在")
			return
		}
		fail(c, http.StatusInternalServerError, "查询失败")
		return
	}
	if !user.CheckPassword(req.OldPassword) {
		badRequest(c, "原密码不正确")
		return
	}
	// 新密码不得与旧密码相同、落库、推口令版本号，均由 applyNewPassword 统一处理；
	// revokeCurrentSession 随后把本次会话一并丢掉
	if ok := h.applyNewPassword(c, &user, req.NewPassword, "密码修改失败"); !ok {
		return
	}
	h.loginLimiter.Reset("chpw:" + ctx.Username)
	h.revokeCurrentSession(c)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// revokeCurrentSession 吊销当前请求携带的访问令牌。
// 口令已变更（或被重置）后，旧会话不应继续可用；公开的重置入口本来就没有有效会话，取不到令牌即空操作。
func (h *Handler) revokeCurrentSession(c *gin.Context) {
	if token, ok := middleware.BearerToken(c); ok {
		h.auth.RevokeToken(token)
	}
}

// GetSecurityInfo GET /auth/security 读取自己的安全设置（不含答案）
func (h *Handler) GetSecurityInfo(c *gin.Context) {
	ctx := currentUser(c)
	var user model.User
	if err := h.db.First(&user, ctx.ID).Error; err != nil {
		notFound(c, "用户不存在")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"password_hint":     user.PasswordHint,
		"security_question": user.SecurityQuestion,
		"has_security":      user.SecurityAnswer != "",
		// 改造前存的是明文，这类存量答案不参与校验，需要用户重设一次
		"answer_legacy": user.IsLegacySecurityAnswer(),
	})
}

// SetSecurityInfo PUT /auth/security 设置/清除密码提示与安全问答（支持部分更新）
func (h *Handler) SetSecurityInfo(c *gin.Context) {
	ctx := currentUser(c)
	var req struct {
		PasswordHint     *string `json:"passwordHint"`
		SecurityQuestion *string `json:"securityQuestion"`
		SecurityAnswer   *string `json:"securityAnswer"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "参数有误")
		return
	}
	updates := map[string]any{}
	if req.PasswordHint != nil {
		hint := strings.TrimSpace(*req.PasswordHint)
		if len([]rune(hint)) > 128 {
			badRequest(c, "密码提示最多 128 字")
			return
		}
		updates["password_hint"] = hint
	}
	if req.SecurityQuestion != nil && req.SecurityAnswer == nil {
		// 只改问题不改答案会让两者对不上，明确拒绝而不是静默留下错配的数据
		badRequest(c, "修改安全问题需同时提交新的答案")
		return
	}
	if req.SecurityAnswer != nil {
		question := ""
		if req.SecurityQuestion != nil {
			question = strings.TrimSpace(*req.SecurityQuestion)
		} else {
			var current model.User
			if err := h.db.Select("security_question").First(&current, ctx.ID).Error; err == nil {
				question = current.SecurityQuestion
			}
		}
		answer := strings.TrimSpace(*req.SecurityAnswer)
		if question == "" && answer != "" {
			badRequest(c, "请选择安全问题")
			return
		}
		if len([]rune(answer)) > 128 {
			badRequest(c, "答案最多 128 字")
			return
		}
		var user model.User
		if err := h.db.Select("id, security_answer").First(&user, ctx.ID).Error; err != nil {
			notFound(c, "用户不存在")
			return
		}
		if err := user.SetSecurityAnswer(answer); err != nil {
			fail(c, http.StatusInternalServerError, "保存失败")
			return
		}
		updates["security_question"] = question
		updates["security_answer"] = user.SecurityAnswer
	}
	if len(updates) == 0 {
		badRequest(c, "没有需要保存的内容")
		return
	}
	if err := h.db.Model(&model.User{}).Where("id = ?", ctx.ID).Updates(updates).Error; err != nil {
		fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetPasswordRecovery GET /auth/forgot?email= 公开：返回密码提示与安全题目。
//
// 无论邮箱是否注册都回 200，未注册时字段为空——接口本身不再充当账号枚举器。
func (h *Handler) GetPasswordRecovery(c *gin.Context) {
	email := strings.ToLower(strings.TrimSpace(c.Query("email")))
	if email == "" {
		badRequest(c, "请输入邮箱")
		return
	}
	if !throttle(c, h.notifyLimiter, "recovery:ip:"+clientID(c)) {
		return
	}
	out := gin.H{
		"exists":            false,
		"password_hint":     "",
		"security_question": "",
		"has_security":      false,
		"answer_legacy":     false,
	}
	var user model.User
	if err := h.db.Where("email = ?", email).First(&user).Error; err == nil && user.IsActive {
		legacy := user.IsLegacySecurityAnswer()
		out["exists"] = true
		out["password_hint"] = user.PasswordHint
		// 存量明文答案不再参与校验，连同题目一起返回只会引导用户去撞一堵墙，
		// 这里置空题目、单独置位 answer_legacy 让前端说明原因
		if !legacy {
			out["security_question"] = user.SecurityQuestion
		}
		out["has_security"] = user.SecurityAnswer != "" && !legacy
		out["answer_legacy"] = user.SecurityAnswer != "" && legacy
	}
	c.JSON(http.StatusOK, out)
}

// ResetPassword POST /auth/forgot/reset 公开：通过安全问题答案重置密码
func (h *Handler) ResetPassword(c *gin.Context) {
	var req struct {
		Email       string `json:"email"`
		Answer      string `json:"answer"`
		NewPassword string `json:"newPassword"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "参数有误")
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" || req.Answer == "" || req.NewPassword == "" {
		badRequest(c, "请填写邮箱、答案和新密码")
		return
	}
	if len(req.NewPassword) < 6 {
		badRequest(c, "新密码至少需要 6 个字符")
		return
	}
	// 答案是最主要的爆破目标，按「IP + 邮箱」双维度限流
	if !throttle(c, h.notifyLimiter, "secans:"+clientID(c)+":"+email) {
		return
	}
	var user model.User
	if err := h.db.Where("email = ?", email).First(&user).Error; err != nil {
		// 与「答案不正确」共用同一句提示，避免用响应差异反推邮箱是否注册
		badRequest(c, "邮箱或安全问题答案不正确")
		return
	}
	if !user.IsActive {
		forbidden(c, "账号已被停用，请联系管理员")
		return
	}
	if user.SecurityQuestion == "" || user.SecurityAnswer == "" {
		badRequest(c, "该账号未设置安全问题，无法通过问答找回密码；请改用邮箱链接或联系管理员")
		return
	}
	if user.IsLegacySecurityAnswer() {
		fail(c, http.StatusLocked, "该账号的安全答案是旧版明文存储，已停止使用，请重新设置安全答案或改用邮箱链接找回密码")
		return
	}
	if !user.CheckSecurityAnswer(req.Answer) {
		badRequest(c, "邮箱或安全问题答案不正确")
		return
	}
	if ok := h.applyNewPassword(c, &user, req.NewPassword, "重置失败"); !ok {
		return
	}
	h.notifyLimiter.Reset("secans:" + clientID(c) + ":" + email)
	h.revokeCurrentSession(c)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// SendForgotEmail POST /auth/forgot/send 公开：向注册邮箱发送密码重置链接
func (h *Handler) SendForgotEmail(c *gin.Context) {
	var req struct {
		Email string `json:"email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "参数有误")
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" {
		badRequest(c, "请输入邮箱")
		return
	}
	// 单邮箱与单 IP 各限一次，堵住「拿一个邮箱慢慢刷」和「一个 IP 扫一堆邮箱」两个方向
	if !throttle(c, h.notifyLimiter, "forgot:mail:"+email) {
		return
	}
	if !throttle(c, h.notifyLimiter, "forgot:ip:"+clientID(c)) {
		return
	}

	// 未配置 SMTP 时不能把重置链接当“开发便利”回给调用方：那是带有效令牌的改密凭证，
	// 任何人都能用一个已知邮箱直接接管账号，限流只是拖慢而已。
	// 只在非生产环境保留该便利；且放在用户查询之前，免得响应差异暴露邮箱是否存在。
	if !h.cfg.SMTPConfigured() && h.cfg.IsProduction() {
		log.Println("[找回密码] 生产环境未配置 SMTP，重置邮件无法发出，请检查 SMTP_HOST/SMTP_USER/SMTP_FROM")
		fail(c, http.StatusServiceUnavailable, "密码重置服务暂不可用，请联系管理员")
		return
	}

	var user model.User
	notFoundOrInactive := h.db.Where("email = ?", email).First(&user).Error != nil || !user.IsActive
	if notFoundOrInactive {
		// 不告诉调用方邮箱存不存在：真发了才发，其余一律回「已发送」
		c.JSON(http.StatusOK, gin.H{"sent": true})
		return
	}

	token, err := h.auth.SignResetToken(user.Email)
	if err != nil {
		fail(c, http.StatusInternalServerError, "生成重置凭证失败")
		return
	}
	resetURL := strings.TrimRight(h.cfg.AppBaseURL, "/") + "/reset-password?token=" + token

	if !h.cfg.SMTPConfigured() {
		// 开发模式：邮件功能未配置，直接返回令牌，便于本地联调
		log.Println("[找回密码] 未配置 SMTP，开发模式生成重置链接：", resetURL)
		c.JSON(http.StatusOK, gin.H{"sent": false, "dev": true, "reset_url": resetURL})
		return
	}

	// SMTP 是外部慢依赖，交给后台队列投递，请求本身不阻塞在发信上
	h.notify.Enqueue(service.TaskNotice{
		Title: "「躬行」密码重置",
		Content: "你好，" + user.FullName + "：\n\n我们收到了你的密码重置申请。请打开以下链接，在 " +
			resetLinkTTLHint + "内完成密码重置：\n\n" + resetURL +
			"\n\n如非本人操作，请忽略此邮件，你的密码不会发生变化。",
		EmailTo: user.Email,
	})
	c.JSON(http.StatusOK, gin.H{"sent": true})
}

// ResetPasswordByToken POST /auth/reset 公开：使用重置令牌设置新密码
func (h *Handler) ResetPasswordByToken(c *gin.Context) {
	var req struct {
		Token       string `json:"token"`
		NewPassword string `json:"newPassword"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "参数有误")
		return
	}
	if req.Token == "" || req.NewPassword == "" {
		badRequest(c, "缺少令牌或新密码")
		return
	}
	if len(req.NewPassword) < 6 {
		badRequest(c, "新密码至少需要 6 个字符")
		return
	}
	if !throttle(c, h.notifyLimiter, "reset:ip:"+clientID(c)) {
		return
	}
	email, err := h.auth.VerifyResetToken(req.Token)
	if err != nil {
		badRequest(c, "重置链接无效或已过期，请重新发起找回")
		return
	}
	var user model.User
	if err := h.db.Where("email = ?", email).First(&user).Error; err != nil {
		notFound(c, "账号不存在")
		return
	}
	if !user.IsActive {
		forbidden(c, "账号已被停用，请联系管理员")
		return
	}
	if ok := h.applyNewPassword(c, &user, req.NewPassword, "重置失败"); !ok {
		return
	}
	// 改密成功后才拉黑令牌：失败时用户仍可重试同一条链接
	h.auth.ConsumeResetToken(req.Token)
	h.revokeCurrentSession(c)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// applyNewPassword 校验并落库新密码。返回 false 表示已写好错误响应，调用方直接 return。
//
// 同时推一次口令版本号（password_changed_at），让改密之前签发的全部会话失效。
func (h *Handler) applyNewPassword(c *gin.Context, user *model.User, newPassword, failMsg string) bool {
	if user.CheckPassword(newPassword) {
		badRequest(c, "新密码不能与原密码相同")
		return false
	}
	if err := user.SetPassword(newPassword); err != nil {
		fail(c, http.StatusInternalServerError, failMsg)
		return false
	}
	changedAt := time.Now().UTC().Truncate(time.Second)
	user.PasswordChangedAt = &changedAt
	if err := h.db.Model(&model.User{}).Where("id = ?", user.ID).Updates(map[string]any{
		"hashed_password":     user.HashedPassword,
		"password_changed_at": changedAt,
	}).Error; err != nil {
		fail(c, http.StatusInternalServerError, failMsg)
		return false
	}
	return true
}
