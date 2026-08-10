package handler

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"sub2api-account-monitor/internal/pkg/response"
	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/server/middleware"
	"sub2api-account-monitor/internal/service"

	"github.com/gin-gonic/gin"
)

// EmbedMediaHandler 生图 / 生视频嵌入页处理器。
//
// 由 sub2api 以 iframe 嵌入，用户身份来自 sub2api 透传的 token（本地共享密钥验签）。
//
// 【身份铁律】用户身份只从会话上下文取（middleware.EmbedUserIDKey），
// 绝不取自请求体或 URL —— 请求结构体里连 user_id 字段都不定义，
// 让「传了也没用」在类型层面成立（与 EmbedKycHandler 同一纪律）。
//
// 【钱的纪律】视频任务提交成功即扣费且不退还。因此：提交端点必须要求幂等键，
// 错误信息必须让用户分清「没扣钱的参数错误」与「已扣钱的审核拒绝」。
type EmbedMediaHandler struct {
	svc    *service.MediaService
	issuer *EmbedSessionIssuer
	pg     *repository.PG
}

// NewEmbedMediaHandler 创建 EmbedMediaHandler。
func NewEmbedMediaHandler(
	svc *service.MediaService,
	verifier *service.Sub2apiTokenVerifier,
	sessions *service.EmbedSessionStore,
	pg *repository.PG,
) *EmbedMediaHandler {
	return &EmbedMediaHandler{
		svc:    svc,
		issuer: NewEmbedSessionIssuer(verifier, sessions, "embed-media"),
		pg:     pg,
	}
}

// CreateSession POST /api/v1/embed/media/session（免鉴权）
func (h *EmbedMediaHandler) CreateSession(c *gin.Context) { h.issuer.Issue(c) }

// mediaTaskDTO 客户侧任务视图。
//
// 刻意不直接返回 repository.MediaTask：那里有 key_fingerprint、group_id
// 等内部字段，复用会让「仓储层加一个字段」自动泄漏到客户侧。
type mediaTaskDTO struct {
	ID           int64  `json:"id"`
	Kind         string `json:"kind"`
	Model        string `json:"model"`
	Prompt       string `json:"prompt"`
	Params       string `json:"params"`
	Status       string `json:"status"`
	Progress     int    `json:"progress"`
	CostUSD      string `json:"cost_usd"`
	EstCostUSD   string `json:"est_cost_usd"`
	ErrorMessage string `json:"error_message"`
	CreatedAt    string `json:"created_at"`
	// HasContent 指示产物是否可通过 /tasks/:id/content 取回。
	//
	// 【只有视频为 true】图片走 b64 随提交响应一次性返回、不落库，
	// 历史图片任务没有可取回的产物。若这里对图片也返回 true，
	// 前端就会渲染出一个必然 404 的预览框——就是修复前的破图。
	HasContent bool `json:"has_content"`
}

func toMediaTaskDTO(t repository.MediaTask) mediaTaskDTO {
	return mediaTaskDTO{
		ID:           t.ID,
		Kind:         t.TaskKind,
		Model:        t.Model,
		Prompt:       t.Prompt,
		Params:       t.ParamsJSON,
		Status:       t.Status,
		Progress:     t.Progress,
		CostUSD:      service.FormatTicksUSD(t.CostTicks),
		EstCostUSD:   service.FormatTicksUSD(t.EstCostTicks),
		ErrorMessage: t.ErrorMessage,
		CreatedAt:    t.CreatedAt,
		HasContent:   t.Status == repository.MediaStatusSucceeded && t.UpstreamRequestID != "",
	}
}

// Keys GET /api/v1/embed/media/keys（需嵌入会话）
// 返回用户可用的 key（掩码）及每把 key 的生成能力。
func (h *EmbedMediaHandler) Keys(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	userID := c.GetString(middleware.EmbedUserIDKey)

	keys, err := h.svc.ListKeys(c.Request.Context(), userID)
	if err != nil {
		response.InternalError(c, "media.errors.loadKeysFailed")
		return
	}
	response.Success(c, gin.H{"items": keys})
}

// mediaGenerateRequest 生成请求（JSON 路径：文生图 / 文生视频 / 图生视频）。
//
// 没有 user_id 字段 —— 身份只从会话取。
type mediaGenerateRequest struct {
	KeyID           int64  `json:"key_id"`
	Kind            string `json:"kind"`
	Model           string `json:"model"`
	Prompt          string `json:"prompt"`
	N               int    `json:"n"`
	Size            string `json:"size"`
	Quality         string `json:"quality"`
	Resolution      string `json:"resolution"`
	Duration        int    `json:"duration"`
	ImageURL        string `json:"image_url"`
	ClientRequestID string `json:"client_request_id"`
}

func (r mediaGenerateRequest) toParams() service.MediaGenerateParams {
	return service.MediaGenerateParams{
		Kind:       r.Kind,
		Model:      r.Model,
		Prompt:     r.Prompt,
		N:          r.N,
		Size:       r.Size,
		Quality:    r.Quality,
		Resolution: r.Resolution,
		Duration:   r.Duration,
		ImageURL:   r.ImageURL,
	}
}

// Generate POST /api/v1/embed/media/generate（需嵌入会话）
// 文生图 / 文生视频 / 图生视频统一入口，按 kind 分流。
func (h *EmbedMediaHandler) Generate(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var req mediaGenerateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "media.errors.invalidParams")
		return
	}
	if req.Kind == service.MediaKindImage2Image {
		// 图生图必须走 multipart 端点：参考图是文件，JSON 装不下
		response.BadRequest(c, "media.errors.useUploadEndpoint")
		return
	}

	h.submit(c, service.MediaSubmitRequest{
		KeyID:           req.KeyID,
		Params:          req.toParams(),
		ClientRequestID: req.ClientRequestID,
	})
}

// maxUploadBytes 单张参考图上限。
//
// 上传文件是流式转发给上游的，不落盘也不全量进内存，但仍要设上限：
// 无上限时一个大文件就能长时间占住一条上游连接。
const maxUploadBytes = 20 << 20 // 20MB

// Edit POST /api/v1/embed/media/edits（需嵌入会话，multipart）
// 图生图：参考图随表单上传，流式转发给上游。
func (h *EmbedMediaHandler) Edit(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	form, err := c.MultipartForm()
	if err != nil {
		response.BadRequest(c, "media.errors.invalidParams")
		return
	}
	headers := form.File["image"]
	if len(headers) == 0 {
		response.BadRequest(c, "media.errors.missingImage")
		return
	}

	files := make([]service.MediaUploadFile, 0, len(headers))
	for _, fh := range headers {
		if fh.Size > maxUploadBytes {
			response.BadRequest(c, "media.errors.imageTooLarge")
			return
		}
		f, err := fh.Open()
		if err != nil {
			response.BadRequest(c, "media.errors.invalidParams")
			return
		}
		defer f.Close()
		files = append(files, service.MediaUploadFile{Filename: fh.Filename, Reader: f})
	}

	keyID, _ := strconv.ParseInt(c.PostForm("key_id"), 10, 64)
	n, _ := strconv.Atoi(c.PostForm("n"))
	if n <= 0 {
		n = 1
	}

	h.submit(c, service.MediaSubmitRequest{
		KeyID: keyID,
		Params: service.MediaGenerateParams{
			Kind:    service.MediaKindImage2Image,
			Model:   c.PostForm("model"),
			Prompt:  c.PostForm("prompt"),
			N:       n,
			Size:    c.PostForm("size"),
			Quality: c.PostForm("quality"),
		},
		ClientRequestID: c.PostForm("client_request_id"),
		Files:           files,
	})
}

// mediaSubmitDTO 提交响应：任务元数据 + 本次生成的图片。
//
// images 只在本次响应里存在一次 —— 图片走 b64 从网关取回后不落库
// （xAI CDN 直链国内不可达，存链接等于存一个打不开的地址）。
// 刷新页面后历史任务只剩元数据，这是「只存元数据」取舍的明确代价。
type mediaSubmitDTO struct {
	Task mediaTaskDTO `json:"task"`
	// Images 是 data URI 列表，前端直接塞进 <img src>。
	Images []string `json:"images"`
}

// submit 执行提交并统一翻译错误。
func (h *EmbedMediaHandler) submit(c *gin.Context, req service.MediaSubmitRequest) {
	userID := c.GetString(middleware.EmbedUserIDKey)

	res, err := h.svc.Submit(c.Request.Context(), userID, req)
	if err != nil {
		h.writeSubmitError(c, err)
		return
	}
	images := res.InlineImages
	if images == nil {
		images = []string{}
	}
	response.Success(c, mediaSubmitDTO{Task: toMediaTaskDTO(*res.Task), Images: images})
}

// writeSubmitError 把提交失败翻译成合适的状态码与提示。
//
// 【必须让用户分清有没有扣钱】上游 4xx 里，参数错误不扣费，
// 而视频审核拒绝已扣费。前端据 media.errors.upstreamRejected 给出显著提示。
func (h *EmbedMediaHandler) writeSubmitError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrMediaKeyNotFound):
		response.BadRequest(c, "media.errors.keyNotFound")
	case errors.Is(err, service.ErrMediaTooManyActive):
		response.BadRequest(c, "media.errors.tooManyActive")
	case errors.Is(err, service.ErrMediaRateLimited):
		response.BadRequest(c, "media.errors.tooFrequent")
	default:
		var ge *service.MediaGatewayError
		if errors.As(err, &ge) {
			// 上游错误信息已脱敏，原样透出便于用户自行调整参数
			response.BadRequest(c, ge.Message)
			return
		}
		// 本地参数校验错误也是可读中文，直接透出
		response.BadRequest(c, err.Error())
	}
}

// Tasks GET /api/v1/embed/media/tasks（需嵌入会话）
// 返回任务列表，并顺手刷新进行中的视频任务状态。
func (h *EmbedMediaHandler) Tasks(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	userID := c.GetString(middleware.EmbedUserIDKey)
	limit, _ := strconv.Atoi(c.Query("limit"))

	list, err := h.svc.ListTasks(c.Request.Context(), userID, limit)
	if err != nil {
		response.InternalError(c, "media.errors.loadTasksFailed")
		return
	}
	items := make([]mediaTaskDTO, 0, len(list))
	for _, t := range list {
		items = append(items, toMediaTaskDTO(t))
	}
	response.Success(c, gin.H{"items": items})
}

// Content GET /api/v1/embed/media/tasks/:id/content（需嵌入会话）
// 产物代理：图片走上游 CDN，视频带认证头走网关。
//
// 【为什么必须代理】视频产物端点强制要求 Authorization 头，而浏览器的
// <video src> 无法携带自定义头；前端也拿不到明文 key（不该拿到）。
func (h *EmbedMediaHandler) Content(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	userID := c.GetString(middleware.EmbedUserIDKey)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "media.errors.invalidParams")
		return
	}

	// 归属校验在 repo 层的 SQL 里，越权访问返回 ErrNotFound
	content, err := h.svc.OpenContent(c.Request.Context(), userID, id)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrNotFound):
			response.NotFound(c, "media.errors.taskNotFound")
		case errors.Is(err, service.ErrMediaNotReady):
			response.BadRequest(c, "media.errors.contentNotReady")
		default:
			response.InternalError(c, "media.errors.contentFailed")
		}
		return
	}
	defer content.Body.Close()

	c.Header("Content-Type", content.ContentType)
	// 产物是私有数据，禁止任何中间层缓存
	c.Header("Cache-Control", "private, no-store")
	c.Status(http.StatusOK)

	// 【流式转发】视频可达数十 MB，io.ReadAll 在并发下会 OOM。
	// 拷贝中途出错时响应头已发出，只能中断连接——记不了错误码，
	// 但客户端会看到不完整响应并重试。
	_, _ = io.Copy(c.Writer, content.Body)
}

// ready 校验服务可用性，未就绪时直接写响应并返回 false。
func (h *EmbedMediaHandler) ready(c *gin.Context) bool {
	if h.svc == nil {
		response.ServiceUnavailable(c, "media.errors.notConfigured")
		return false
	}
	if h.pg != nil && !h.pg.Available() {
		response.ServiceUnavailable(c, "plaza.errors.databaseUnavailable")
		return false
	}
	return true
}
