package handler

import (
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"taskbackend/internal/model"
	"taskbackend/internal/service"
)

// 发送找回邮件的最小间隔（防止刷接口）
var forgotSendLocks sync.Map

// getSetting / setSetting 读写系统设置
func (h *Handler) getSetting(key, def string) string {
	var s model.SystemSetting
	if err := h.db.Where("key = ?", key).First(&s).Error; err != nil {
		return def
	}
	return s.Value
}

func (h *Handler) setSetting(key, value string) error {
	return h.db.Save(&model.SystemSetting{Key: key, Value: value}).Error
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
	var user model.User
	if err := h.db.First(&user, ctx.ID).Error; err != nil {
		notFound(c, "用户不存在")
		return
	}
	if !user.CheckPassword(req.OldPassword) {
		badRequest(c, "原密码不正确")
		return
	}
	if user.CheckPassword(req.NewPassword) {
		badRequest(c, "新密码不能与原密码相同")
		return
	}
	if err := user.SetPassword(req.NewPassword); err != nil {
		fail(c, http.StatusInternalServerError, "密码修改失败")
		return
	}
	if err := h.db.Model(&model.User{}).Where("id = ?", ctx.ID).Update("hashed_password", user.HashedPassword).Error; err != nil {
		fail(c, http.StatusInternalServerError, "密码修改失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
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
	updates := map[string]interface{}{}
	if req.PasswordHint != nil {
		updates["password_hint"] = strings.TrimSpace(*req.PasswordHint)
	}
	if req.SecurityAnswer != nil {
		question := ""
		if req.SecurityQuestion != nil {
			question = strings.TrimSpace(*req.SecurityQuestion)
		}
		answer := strings.ToLower(strings.TrimSpace(*req.SecurityAnswer))
		if question == "" && answer != "" {
			badRequest(c, "请选择安全问题")
			return
		}
		updates["security_question"] = question
		updates["security_answer"] = answer
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

// GetPasswordRecovery GET /auth/forgot?email= 公开：根据邮箱返回密码提示词与安全问题（不含答案）
func (h *Handler) GetPasswordRecovery(c *gin.Context) {
	email := strings.ToLower(strings.TrimSpace(c.Query("email")))
	if email == "" {
		badRequest(c, "请输入邮箱")
		return
	}
	var user model.User
	if err := h.db.Where("email = ?", email).First(&user).Error; err != nil {
		notFound(c, "该邮箱未注册")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"exists":            true,
		"password_hint":     user.PasswordHint,
		"security_question": user.SecurityQuestion,
		"has_security":      user.SecurityAnswer != "",
	})
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
	var user model.User
	if err := h.db.Where("email = ?", email).First(&user).Error; err != nil {
		notFound(c, "该邮箱未注册")
		return
	}
	if user.SecurityAnswer == "" || user.SecurityQuestion == "" {
		badRequest(c, "该账号未设置安全问题，无法通过问答找回密码；请联系管理员")
		return
	}
	if strings.ToLower(strings.TrimSpace(req.Answer)) != user.SecurityAnswer {
		badRequest(c, "安全问题回答不正确")
		return
	}
	if user.CheckPassword(req.NewPassword) {
		badRequest(c, "新密码不能与原密码相同")
		return
	}
	if err := user.SetPassword(req.NewPassword); err != nil {
		fail(c, http.StatusInternalServerError, "重置失败")
		return
	}
	if err := h.db.Model(&model.User{}).Where("id = ?", user.ID).Update("hashed_password", user.HashedPassword).Error; err != nil {
		fail(c, http.StatusInternalServerError, "重置失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// SendForgotEmail POST /auth/forgot/send 公开：向注册邮箱发送密码重置链接（有效期 30 分钟）
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

	// 频率限制：同一邮箱 60 秒内只能发送一次
	if last, ok := forgotSendLocks.Load(email); ok {
		if t, ok2 := last.(time.Time); ok2 && time.Since(t) < time.Minute {
			fail(c, http.StatusTooManyRequests, "已发送过，请 1 分钟后再试")
			return
		}
	}

	var user model.User
	if err := h.db.Where("email = ?", email).First(&user).Error; err != nil {
		notFound(c, "该邮箱未注册")
		return
	}
	if !user.IsActive {
		forbidden(c, "账号已被停用，请联系管理员")
		return
	}

	token, err := h.auth.SignResetToken(user.Email)
	if err != nil {
		fail(c, http.StatusInternalServerError, "生成重置凭证失败")
		return
	}
	resetURL := strings.TrimRight(h.cfg.AppBaseURL, "/") + "/reset-password?token=" + token
	forgotSendLocks.Store(email, time.Now())

	if h.cfg.SMTPHost == "" || h.cfg.SMTPUser == "" || h.cfg.SMTPFrom == "" {
		// 开发模式：邮件功能未配置，直接返回令牌，便于本地联调
		log.Println("[找回密码] 未配置 SMTP，开发模式生成重置链接：", resetURL)
		c.JSON(http.StatusOK, gin.H{"sent": false, "dev": true, "reset_url": resetURL})
		return
	}

	subject := "「躬行」密码重置"
	body := "你好，" + user.FullName + "：\n\n我们收到了你的密码重置申请。请打开以下链接，在 30 分钟内完成密码重置：\n\n" +
		resetURL + "\n\n如非本人操作，请忽略此邮件，你的密码不会发生变化。"
	if err := service.SendEmail(h.cfg, user.Email, subject, body); err != nil {
		log.Printf("[找回密码] 发送邮件失败：%v", err)
		fail(c, http.StatusInternalServerError, "邮件发送失败，请稍后重试或联系管理员")
		return
	}
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
	if user.CheckPassword(req.NewPassword) {
		badRequest(c, "新密码不能与原密码相同")
		return
	}
	if err := user.SetPassword(req.NewPassword); err != nil {
		fail(c, http.StatusInternalServerError, "重置失败")
		return
	}
	if err := h.db.Model(&model.User{}).Where("id = ?", user.ID).Update("hashed_password", user.HashedPassword).Error; err != nil {
		fail(c, http.StatusInternalServerError, "重置失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
