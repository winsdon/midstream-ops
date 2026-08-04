package handler

import (
	"strconv"
	"strings"
	"time"

	"sub2api-account-monitor/internal/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// devTokenTTL 是调试 token 的有效期。
//
// 刻意短：调试 token 会进浏览器地址栏与历史记录，1 小时足够一轮联调，
// 过期后重新从调试页点一次即可，成本极低。
const devTokenTTL = time.Hour

// EmbedDevHandler 为本地联调签发 sub2api 用户 token。
//
// 【仅在 plaza.dev_mode = true 时注册，生产绝不可开】
// 它用与验签同一把密钥（plaza.sub2api_jwt_secret）签出任意 user_id 的合法 token，
// 等同于「无密码登录成任何客户」。之所以能这么做而不引入新风险：本站本就持有该密钥
// 用于验签（见 config.PlazaConfig 注释），此处只是把既有的签名能力暴露成 HTTP 接口。
//
// 存在的意义：嵌入页的身份来自 sub2api iframe 透传的 token，本地开发时没有 sub2api
// 站点可依赖。没有它，测试 /embed/* 只能手工拼 URL 或搭一整套上游环境。
type EmbedDevHandler struct {
	secret string
}

// NewEmbedDevHandler 创建调试签发器。secret 即 plaza.sub2api_jwt_secret。
func NewEmbedDevHandler(secret string) *EmbedDevHandler {
	return &EmbedDevHandler{secret: strings.TrimSpace(secret)}
}

type devTokenResponse struct {
	Token  string `json:"token"`
	UserID string `json:"user_id"`
	Email  string `json:"email"`
}

// IssueToken 签发一个可通过本站验签的 sub2api 用户 token。
//
// 查询参数：user_id（默认 1）、email（默认 dev@local）。
// 刻意只签 Verify 实际校验的字段（user_id / email / exp）——sub2api 真实 token 里的
// sid / bnd / token_version 本站不校验，补上它们只会让人误以为这是等价的真 token。
func (h *EmbedDevHandler) IssueToken(c *gin.Context) {
	if h.secret == "" {
		response.ServiceUnavailable(c, "plaza.errors.notConfigured")
		return
	}

	userID := int64(1)
	if raw := strings.TrimSpace(c.Query("user_id")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		// 必须 > 0：Verify 会拒绝 user_id <= 0，这里提前挡住给出可读错误。
		if err != nil || parsed <= 0 {
			response.BadRequest(c, "plaza.errors.missingParams")
			return
		}
		userID = parsed
	}

	email := strings.TrimSpace(c.Query("email"))
	if email == "" {
		email = "dev@local"
	}

	claims := jwt.MapClaims{
		"user_id": userID,
		"email":   email,
		"exp":     time.Now().Add(devTokenTTL).Unix(),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(h.secret))
	if err != nil {
		response.InternalError(c, "plaza.errors.sessionCreateFailed")
		return
	}

	response.Success(c, devTokenResponse{
		Token:  signed,
		UserID: strconv.FormatInt(userID, 10),
		Email:  email,
	})
}
