// Package handler 提供 HTTP 处理器。
package handler

import (
	"errors"

	"sub2api-account-monitor/internal/pkg/response"
	"sub2api-account-monitor/internal/service"

	"github.com/gin-gonic/gin"
)

// AuthHandler 认证处理器。
type AuthHandler struct {
	authSvc *service.AuthService
}

// NewAuthHandler 创建 AuthHandler。
func NewAuthHandler(authSvc *service.AuthService) *AuthHandler {
	return &AuthHandler{authSvc: authSvc}
}

type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// Login POST /auth/login
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "username 与 password 必填")
		return
	}
	result, err := h.authSvc.Login(req.Username, req.Password)
	if err != nil {
		if errors.Is(err, service.ErrInvalidCredentials) {
			response.Unauthorized(c, "用户名或密码错误")
			return
		}
		response.InternalError(c, "登录失败")
		return
	}
	response.Success(c, result)
}

// Me GET /auth/me
func (h *AuthHandler) Me(c *gin.Context) {
	username, _ := c.Get("username")
	response.Success(c, gin.H{"username": username})
}
