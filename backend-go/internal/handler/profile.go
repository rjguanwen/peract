package handler

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"taskbackend/internal/model"
)

// avatarExtForType 允许的头像类型及其落盘扩展名。
// 表里的键同时是「嗅探结果」，不接受任何客户端声明的类型。
var avatarExtForType = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

const (
	maxAvatarSize   = 5 * 1024 * 1024 // 5MB
	avatarSniffSize = 512             // 魔数识别读取的文件头长度
)

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
	updates := map[string]any{}
	if req.FullName != nil {
		name := strings.TrimSpace(*req.FullName)
		if len([]rune(name)) > maxFullNameLen {
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
	var user model.User
	if err := h.db.Model(&model.User{}).Where("id = ?", ctx.ID).Updates(updates).Error; err != nil {
		fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	if err := h.db.First(&user, ctx.ID).Error; err != nil {
		fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
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
	if fileHeader.Size > maxAvatarSize {
		badRequest(c, "头像图片不能超过 5MB")
		return
	}

	src, err := fileHeader.Open()
	if err != nil {
		badRequest(c, "读取上传文件失败")
		return
	}
	defer src.Close()
	head := make([]byte, avatarSniffSize)
	n, err := io.ReadFull(src, head)
	if err != nil && n == 0 {
		badRequest(c, "上传文件为空")
		return
	}
	head = head[:n]

	// 类型判定完全基于文件头魔数。客户端的 Content-Type 与文件名扩展名都是可伪造的请求内容，
	// 而上传目录直接对外静态暴露——放一个伪装成 png 的 HTML 进去就是存储型 XSS。
	mimeType := detectImageType(head)
	ext, ok := avatarExtForType[mimeType]
	if !ok {
		badRequest(c, "头像仅支持 jpg/png/gif/webp 图片")
		return
	}

	dir := filepath.Join(h.uploadDir, "avatars")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		fail(c, http.StatusInternalServerError, "创建目录失败")
		return
	}
	// 文件名由服务端生成：用户提供的文件名一个字符都不参与落盘路径
	fileName := fmt.Sprintf("u%d_%d%s", ctx.ID, time.Now().UnixNano(), ext)
	dest := filepath.Join(dir, fileName)
	if err := c.SaveUploadedFile(fileHeader, dest); err != nil {
		fail(c, http.StatusInternalServerError, "保存头像失败")
		return
	}
	newURL := "/uploads/avatars/" + fileName

	var user model.User
	if err := h.db.Select("id, avatar_url").First(&user, ctx.ID).Error; err != nil {
		_ = os.Remove(dest)
		notFound(c, "用户不存在")
		return
	}
	if err := h.db.Model(&model.User{}).Where("id = ?", ctx.ID).
		Update("avatar_url", newURL).Error; err != nil {
		_ = os.Remove(dest)
		fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	// 换图成功后再删旧文件，并且只在路径确实落在本应用上传目录内时才删
	if old := user.AvatarURL; old != nil && *old != "" && *old != newURL {
		if p, ok := h.uploadFilePath(*old); ok {
			_ = os.Remove(p)
		}
	}
	if err := h.db.First(&user, ctx.ID).Error; err != nil {
		fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	c.JSON(http.StatusOK, toUserOut(&user))
}

// detectImageType 用文件头魔数判定图片类型，无法识别时返回空串。
//
// 不用 http.DetectContentType：它把 webp 报成 application/octet-stream，
// 且对 jpeg/png 之外的格式识别粒度不够，这里要的是明确的白名单语义。
func detectImageType(head []byte) string {
	switch {
	case len(head) >= 3 && head[0] == 0xFF && head[1] == 0xD8 && head[2] == 0xFF:
		return "image/jpeg"
	case len(head) >= 8 && string(head[:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png"
	case len(head) >= 6 && string(head[:3]) == "GIF" &&
		(string(head[3:6]) == "87a" || string(head[3:6]) == "89a"):
		return "image/gif"
	case len(head) >= 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WEBP":
		return "image/webp"
	}
	return ""
}

// uploadFilePath 把对外的 /uploads/... URL 解析成本地路径，
// 拒绝任何越出上传根目录的结果（历史上写入的脏数据或手工改过的字段都可能带 ..）。
func (h *Handler) uploadFilePath(url string) (string, bool) {
	const prefix = "/uploads/"
	if !strings.HasPrefix(url, prefix) {
		return "", false
	}
	rel := filepath.FromSlash(strings.TrimPrefix(url, prefix))
	if filepath.IsAbs(rel) || rel == "" {
		return "", false
	}
	full := filepath.Clean(filepath.Join(h.uploadDir, rel))
	root := filepath.Clean(h.uploadDir)
	if full != root && !strings.HasPrefix(full, root+string(os.PathSeparator)) {
		return "", false
	}
	return full, true
}
