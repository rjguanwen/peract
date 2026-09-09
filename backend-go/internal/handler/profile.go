package handler

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"taskbackend/internal/model"
)

// avatarImageTypes 头像允许的图片类型
var avatarImageTypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true,
}

const maxAvatarSize = 5 * 1024 * 1024 // 5MB

// UpdateProfile PUT /profile 更新个人资料（昵称/姓名、个性签名）
func (h *Handler) UpdateProfile(c *gin.Context) {
	ctx := currentUser(c)
	var req struct {
		FullName  *string `json:"full_name"`
		Signature *string `json:"signature"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "参数有误")
		return
	}
	var user model.User
	if err := h.db.First(&user, ctx.ID).Error; err != nil {
		notFound(c, "用户不存在")
		return
	}
	updates := map[string]interface{}{}
	if req.FullName != nil {
		name := strings.TrimSpace(*req.FullName)
		if len([]rune(name)) > 128 {
			badRequest(c, "昵称最多 128 字")
			return
		}
		updates["full_name"] = name
	}
	if req.Signature != nil {
		sig := strings.TrimSpace(*req.Signature)
		if len([]rune(sig)) > 80 {
			badRequest(c, "个性签名最多 80 字")
			return
		}
		updates["signature"] = sig
	}
	if len(updates) == 0 {
		badRequest(c, "没有需要保存的内容")
		return
	}
	if err := h.db.Model(&model.User{}).Where("id = ?", ctx.ID).Updates(updates).Error; err != nil {
		fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	h.db.First(&user, ctx.ID)
	c.JSON(http.StatusOK, toUserOut(&user))
}

// UploadAvatar PUT /profile/avatar 上传当前用户头像（multipart: file）。头像存 uploads/avatars/，替换时删除旧文件。
func (h *Handler) UploadAvatar(c *gin.Context) {
	ctx := currentUser(c)

	fileHeader, err := c.FormFile("file")
	if err != nil {
		badRequest(c, "请选择要上传的图片")
		return
	}
	mimeType := fileHeader.Header.Get("Content-Type")
	if !avatarImageTypes[mimeType] {
		badRequest(c, "头像仅支持 jpg/png/gif/webp 图片")
		return
	}
	if fileHeader.Size > maxAvatarSize {
		badRequest(c, "头像图片不能超过 5MB")
		return
	}

	dir := filepath.Join(h.uploadDir, "avatars")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(c, http.StatusInternalServerError, "创建目录失败")
		return
	}
	ext := strings.ToLower(filepath.Ext(fileHeader.Filename))
	if ext == "" {
		ext = extForMimeAvatar(mimeType)
	}
	fileName := fmt.Sprintf("u%d_%d%s", ctx.ID, time.Now().UnixNano(), ext)
	if err := c.SaveUploadedFile(fileHeader, filepath.Join(dir, fileName)); err != nil {
		fail(c, http.StatusInternalServerError, "保存头像失败")
		return
	}
	newURL := "/uploads/avatars/" + fileName

	var user model.User
	if err := h.db.First(&user, ctx.ID).Error; err != nil {
		_ = os.Remove(filepath.Join(dir, fileName))
		notFound(c, "用户不存在")
		return
	}
	// 删除旧头像（仅当旧头像确实位于本应用的 avatars 目录）
	if user.AvatarURL != nil && strings.HasPrefix(*user.AvatarURL, "/uploads/avatars/") {
		_ = os.Remove(filepath.Join(h.uploadDir, strings.TrimPrefix(*user.AvatarURL, "/uploads/")))
	}
	if err := h.db.Model(&model.User{}).Where("id = ?", ctx.ID).
		Update("avatar_url", newURL).Error; err != nil {
		_ = os.Remove(filepath.Join(dir, fileName))
		fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	h.db.First(&user, ctx.ID)
	c.JSON(http.StatusOK, toUserOut(&user))
}

func extForMimeAvatar(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	}
	return ".bin"
}
