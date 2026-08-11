// Package response 提供统一的 API 响应封装（{code,message,data} + 分页）。
package response

import (
	"math"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Response 标准 API 响应格式。
type Response struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// PaginatedData 分页数据格式。
type PaginatedData struct {
	Items    any   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Pages    int   `json:"pages"`
}

// Success 返回成功响应。
func Success(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Response{Code: 0, Message: "success", Data: data})
}

// Error 返回错误响应。
func Error(c *gin.Context, statusCode int, message string) {
	c.JSON(statusCode, Response{Code: statusCode, Message: message})
}

// BadRequest 400。
func BadRequest(c *gin.Context, message string) { Error(c, http.StatusBadRequest, message) }

// Unauthorized 401。
func Unauthorized(c *gin.Context, message string) { Error(c, http.StatusUnauthorized, message) }

// Forbidden 403。
func Forbidden(c *gin.Context, message string) { Error(c, http.StatusForbidden, message) }

// NotFound 404。
func NotFound(c *gin.Context, message string) { Error(c, http.StatusNotFound, message) }

// Conflict 409（唯一键冲突，如重复建档）。
func Conflict(c *gin.Context, message string) { Error(c, http.StatusConflict, message) }

// InternalError 500。
func InternalError(c *gin.Context, message string) { Error(c, http.StatusInternalServerError, message) }

// ServiceUnavailable 503（用于 PG 不可用时）。
func ServiceUnavailable(c *gin.Context, message string) {
	Error(c, http.StatusServiceUnavailable, message)
}

// Paginated 返回分页数据。
func Paginated(c *gin.Context, items any, total int64, page, pageSize int) {
	pages := int(math.Ceil(float64(total) / float64(pageSize)))
	if pages < 1 {
		pages = 1
	}
	Success(c, PaginatedData{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Pages:    pages,
	})
}

// ParsePagination 解析分页参数（page / page_size|limit）。
func ParsePagination(c *gin.Context) (page, pageSize int) {
	page = 1
	pageSize = 20
	if p := c.Query("page"); p != "" {
		if val, err := parseInt(p); err == nil && val > 0 {
			page = val
		}
	}
	if ps := c.Query("page_size"); ps != "" {
		if val, err := parseInt(ps); err == nil && val > 0 && val <= 1000 {
			pageSize = val
		}
	} else if l := c.Query("limit"); l != "" {
		if val, err := parseInt(l); err == nil && val > 0 && val <= 1000 {
			pageSize = val
		}
	}
	return page, pageSize
}

func parseInt(s string) (int, error) {
	var result int
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, errInvalidInt
		}
		result = result*10 + int(ch-'0')
	}
	return result, nil
}

var errInvalidInt = &parseError{"invalid int"}

type parseError struct{ s string }

func (e *parseError) Error() string { return e.s }
