package handler

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"taskbackend/internal/model"
	"taskbackend/internal/service"
)

// 邀请链接有效期（天）
const inviteValidDays = 7

// randomToken 生成 URL 安全的随机邀请令牌
func randomToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return strings.ReplaceAll(time.Now().Format("20060102150405.000000000"), ".", "") + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(b)
}

// invitationExpired 判断邀请是否已过期
func invitationExpired(expiresAt time.Time) bool {
	return time.Now().After(expiresAt)
}

// markInviteUsed 将该邮箱处于"待接受"的邀请标记为已使用
func (h *Handler) markInviteUsed(email string) {
	h.db.Model(&model.Invitation{}).
		Where("email = ? AND status = ?", email, model.InviteStatusPending).
		Updates(map[string]interface{}{
			"status":  model.InviteStatusRegistered,
			"used_at": time.Now(),
		})
}

// smtpConfigured SMTP 是否完成配置
func (h *Handler) smtpConfigured() bool {
	return h.cfg.SMTPHost != "" && h.cfg.SMTPUser != "" && h.cfg.SMTPFrom != ""
}

// InviteInfo GET /auth/invite/info 公开：校验邀请令牌并返回受邀邮箱与邀请人，供注册页预填
func (h *Handler) InviteInfo(c *gin.Context) {
	token := strings.TrimSpace(c.Query("token"))
	if token == "" {
		badRequest(c, "缺少邀请令牌")
		return
	}
	var inv model.Invitation
	if err := h.db.Where("token = ?", token).First(&inv).Error; err != nil {
		badRequest(c, "邀请链接无效，请联系管理员")
		return
	}
	if invitationExpired(inv.ExpiresAt) {
		badRequest(c, "邀请链接已过期，请联系管理员重新邀请")
		return
	}
	switch inv.Status {
	case model.InviteStatusRegistered:
		badRequest(c, "该邀请已被使用，请直接登录")
		return
	case model.InviteStatusRevoked:
		badRequest(c, "该邀请已被撤销，请联系管理员")
		return
	}
	inviterName := ""
	var inviter model.User
	if err := h.db.First(&inviter, inv.InvitedBy).Error; err == nil {
		inviterName = inviter.FullName
		if inviterName == "" {
			inviterName = inviter.Username
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"email":                inv.Email,
		"invitedByDisplayName": inviterName,
		"expiresAt":            inv.ExpiresAt.Format("2006-01-02 15:04:05"),
	})
}

// GetSettings GET /admin/settings 读取系统设置（注册开关等，仅管理员）
func (h *Handler) GetSettings(c *gin.Context) {
	enabled := h.getSetting(model.SettingRegistrationEnabled, "true") == "true"
	c.JSON(http.StatusOK, gin.H{"registration_enabled": enabled})
}

// SetRegistrationEnabled PUT /admin/settings/registration 设置注册开关（仅管理员）
func (h *Handler) SetRegistrationEnabled(c *gin.Context) {
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		badRequest(c, "缺少 enabled 参数")
		return
	}
	val := "false"
	if *req.Enabled {
		val = "true"
	}
	if err := h.setSetting(model.SettingRegistrationEnabled, val); err != nil {
		fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"registration_enabled": *req.Enabled})
}

// CreateInvites POST /admin/invites 创建邀请并向指定邮箱发送邀请邮件（仅管理员）
// body: {"emails": ["a@x.com", "b@x.com"]}
func (h *Handler) CreateInvites(c *gin.Context) {
	ctx := currentUser(c)
	var me model.User
	meName := ""
	if err := h.db.First(&me, ctx.ID).Error; err == nil {
		meName = me.FullName
		if meName == "" {
			meName = me.Username
		}
	}
	var req struct {
		Emails []string `json:"emails"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Emails) == 0 {
		badRequest(c, "请至少提供一个邮箱")
		return
	}
	if len(req.Emails) > 20 {
		badRequest(c, "单次最多邀请 20 个邮箱")
		return
	}

	results := make([]gin.H, 0, len(req.Emails))
	for _, raw := range req.Emails {
		email := strings.ToLower(strings.TrimSpace(raw))
		res := gin.H{"email": email, "ok": false}

		if email == "" || !emailRe.MatchString(email) {
			res["reason"] = "邮箱格式不正确"
			results = append(results, res)
			continue
		}
		var count int64
		h.db.Model(&model.User{}).Where("email = ?", email).Count(&count)
		if count > 0 {
			res["reason"] = "该邮箱已注册"
			results = append(results, res)
			continue
		}

		token := randomToken()
		now := time.Now()
		expiresAt := now.AddDate(0, 0, inviteValidDays)

		var existing model.Invitation
		hasExisting := h.db.Where("email = ?", email).First(&existing).Error == nil
		snap := existing
		if hasExisting {
			if err := h.db.Model(&model.Invitation{}).Where("id = ?", existing.ID).Updates(map[string]interface{}{
				"token":      token,
				"invited_by": ctx.ID,
				"status":     model.InviteStatusPending,
				"expires_at": expiresAt,
				"created_at": existing.CreatedAt,
				"used_at":    nil,
			}).Error; err != nil {
				res["reason"] = "保存邀请失败"
				results = append(results, res)
				continue
			}
		} else {
			inv := &model.Invitation{
				Email:     email,
				Token:     token,
				InvitedBy: ctx.ID,
				Status:    model.InviteStatusPending,
				ExpiresAt: expiresAt,
				CreatedAt: now,
			}
			if err := h.db.Create(inv).Error; err != nil {
				res["reason"] = "保存邀请失败"
				results = append(results, res)
				continue
			}
		}

		inviteURL := strings.TrimRight(h.cfg.AppBaseURL, "/") + "/register?invite=" + token

		if !h.smtpConfigured() {
			// 开发模式：未配置 SMTP，不真实发信，直接返回邀请链接便于本地测试
			log.Println("[邀请注册] 未配置 SMTP，开发模式生成邀请链接：", inviteURL)
			res["ok"] = true
			res["dev"] = true
			res["invite_url"] = inviteURL
			results = append(results, res)
			continue
		}

		subject := "「任务管理系统」邀请你加入"
		body := "你好：\n\n" + meName + " 邀请你加入「任务管理系统」。\n\n请打开以下链接完成注册（链接 7 天内有效，且仅限本邮件送达的邮箱 " +
			email + " 使用）：\n\n" + inviteURL + "\n\n如非本人操作，请忽略此邮件。"
		if err := service.SendEmail(h.cfg, email, subject, body); err != nil {
			log.Printf("[邀请注册] 发送邀请邮件失败：%v", err)
			// 回滚：新建的删除记录，已存在的恢复其原状态，避免留下不可达的邀请
			if hasExisting {
				h.db.Model(&model.Invitation{}).Where("id = ?", existing.ID).Updates(map[string]interface{}{
					"token":      snap.Token,
					"invited_by": snap.InvitedBy,
					"status":     snap.Status,
					"expires_at": snap.ExpiresAt,
					"created_at": snap.CreatedAt,
					"used_at":    snap.UsedAt,
				})
			} else {
				h.db.Delete(&model.Invitation{}, "token = ?", token)
			}
			res["reason"] = "邮件发送失败，请检查 SMTP 配置"
			results = append(results, res)
			continue
		}

		res["ok"] = true
		results = append(results, res)
	}
	c.JSON(http.StatusOK, gin.H{"results": results})
}

// ListInvites GET /admin/invites 邀请记录列表（仅管理员）
func (h *Handler) ListInvites(c *gin.Context) {
	var invites []model.Invitation
	if err := h.db.Order("id DESC").Find(&invites).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询失败")
		return
	}

	inviterNames := map[uint]gin.H{}
	seen := map[uint]bool{}
	var uidList []uint
	for _, iv := range invites {
		if !seen[iv.InvitedBy] {
			seen[iv.InvitedBy] = true
			uidList = append(uidList, iv.InvitedBy)
		}
	}
	if len(uidList) > 0 {
		var users []model.User
		if err := h.db.Where("id IN ?", uidList).Find(&users).Error; err == nil {
			for _, u := range users {
				name := u.FullName
				if name == "" {
					name = u.Username
				}
				inviterNames[u.ID] = gin.H{"id": u.ID, "email": u.Email, "display_name": name}
			}
		}
	}

	items := make([]gin.H, 0, len(invites))
	for _, iv := range invites {
		item := gin.H{
			"id":         iv.ID,
			"email":      iv.Email,
			"status":     iv.Status,
			"invited_by": iv.InvitedBy,
			"createdAt":  iv.CreatedAt.Format("2006-01-02T15:04:05"),
			"expiresAt":  iv.ExpiresAt.Format("2006-01-02T15:04:05"),
			"expired":    iv.Status == model.InviteStatusPending && invitationExpired(iv.ExpiresAt),
		}
		if iv.UsedAt != nil {
			item["usedAt"] = iv.UsedAt.Format("2006-01-02T15:04:05")
		} else {
			item["usedAt"] = nil
		}
		if n, ok := inviterNames[iv.InvitedBy]; ok {
			item["invitedBy"] = n
		}
		items = append(items, item)
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": len(items)})
}

// RevokeInvite POST /admin/invites/:id/revoke 撤销一条待接受的邀请（仅管理员）
func (h *Handler) RevokeInvite(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		badRequest(c, "无效的 id")
		return
	}
	var inv model.Invitation
	if err := h.db.First(&inv, id).Error; err != nil {
		notFound(c, "邀请记录不存在")
		return
	}
	if inv.Status != model.InviteStatusPending {
		badRequest(c, "仅待接受的邀请可以撤销")
		return
	}
	if err := h.db.Model(&model.Invitation{}).Where("id = ?", id).Update("status", model.InviteStatusRevoked).Error; err != nil {
		fail(c, http.StatusInternalServerError, "操作失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
