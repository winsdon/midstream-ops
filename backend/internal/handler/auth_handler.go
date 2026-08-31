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

func (h *AuthHandler) ready(c *gin.Context) bool {
	if h == nil || h.authSvc == nil {
		response.InternalError(c, "auth 未配置")
		return false
	}
	return true
}

// Login POST /auth/login
func (h *AuthHandler) Login(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "username 与 password 必填")
		return
	}
	result, err := h.authSvc.Login(c.Request.Context(), req.Username, req.Password)
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

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// Refresh POST /auth/refresh
func (h *AuthHandler) Refresh(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "refresh_token 必填")
		return
	}
	result, err := h.authSvc.Refresh(c.Request.Context(), req.RefreshToken)
	if err != nil {
		if errors.Is(err, service.ErrRefreshTokenInvalid) || errors.Is(err, service.ErrRefreshTokenExpired) {
			response.Unauthorized(c, "refresh token 无效或已过期")
			return
		}
		response.InternalError(c, "续期失败")
		return
	}
	response.Success(c, result)
}

type logoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// Logout POST /auth/logout
func (h *AuthHandler) Logout(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var req logoutRequest
	_ = c.ShouldBindJSON(&req)
	if err := h.authSvc.Logout(c.Request.Context(), req.RefreshToken); err != nil {
		response.InternalError(c, "登出失败")
		return
	}
	response.Success(c, gin.H{"message": "ok"})
}

// Me GET /auth/me
func (h *AuthHandler) Me(c *gin.Context) {
	username, _ := c.Get("username")
	response.Success(c, gin.H{"username": username})
}
