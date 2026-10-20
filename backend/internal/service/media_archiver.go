package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"sub2api-account-monitor/internal/pkg/objectstore"
	"sub2api-account-monitor/internal/repository"
)

// mediaArchiveConcurrency 同时进行的视频转存数上限。
//
// 视频动辄几十 MB，每条转存同时占着一条上游下载连接与一条 R2 上传连接。
// 不限并发时，十个任务同时完成就是二十条长连接和相应的内存缓冲。
// 取 2：单二进制部署，带宽是瓶颈，再多也只是让每条都变慢。
const mediaArchiveConcurrency = 2

// mediaArchiveTimeout 单次视频转存的整体超时。
//
// 没有上限的转存会永久占住一个并发槽。给足 15 分钟——跨境上传几十 MB 够用，
// 超过就说明网络已经不可用，重试比干等划算。
const mediaArchiveTimeout = 15 * time.Minute

// 图片产物的「回源」参数：上游偶尔只回 URL、不回 b64_json（见 imageBytes）。
//
// 下载在同步提交路径上，必须有时限——挂死的连接会把用户在生成页上锁到
// 浏览器自己超时。32MB 上限是为了不把「上游给了一个非图片的大文件」这种
// 异常当成正常产物整个读进内存；gpt-image 的 4K PNG 实测 10MB 量级，
// 留足余量。
const (
	mediaImageDownloadTimeout  = 2 * time.Minute
	mediaImageDownloadMaxBytes = 32 << 20

	// mediaImagePutTimeout 单张图片单次上传对象存储的上限。
	//
	// 图片只有几百 KB ~ 十几 MB，跨境一分钟足够；R2 客户端整体超时是 10 分钟，
	// 那是给几十 MB 的视频留的。把那个值套在同步生图路径上，一次挂死的上传
	// 会让用户一直等到浏览器自己超时。
	mediaImagePutTimeout = 60 * time.Second

	// mediaArchiveAttempts 单张图片「上传」与「产物记录落库」各自的尝试次数（含首次）。
	//
	// 转存失败 = 用户刷新后永久丢图，而对象存储偶发 5xx/断连、数据库偶发连接抖动
	// 都是可重试的；多传一次的成本远低于一次误判。落库用 UPSERT，重试幂等。
	mediaArchiveAttempts = 2
)

// mediaArchiver 视频产物转存器。
//
// 【为什么视频要异步而图片同步】图片单张几百 KB，转存耗时相对生成本身
// （5-10 秒）可忽略，放在提交路径上不影响体感。而视频「已完成」是在用户轮询
// 任务列表的同步请求里被发现的——在那里转存几十 MB 会把一次本该几十毫秒的
// 列表查询拖成几十秒。
//
// 【为什么不是每次起一个裸 goroutine】那样既没有并发上限，也无法防止同一任务
// 被连续几轮轮询重复投递（列表刷新期间 storage_status 还是 pending）。
type mediaArchiver struct {
	tasks     *repository.MediaTaskRepo
	artifacts *repository.MediaArtifactRepo
	gateway   *MediaGateway
	uploader  objectstore.Uploader

	// imageFetch 回源下载上游图片 URL 的客户端。
	//
	// 【为什么不能直接用 http.DefaultClient】产物流地址来自上游响应体，属于
	// 不可信输入。这里复用参考图下载客户端：它在拨号时解析并固定公网 IP、
	// 重定向逐跳复用同一校验，避免把上游给的内网地址变成一条 SSRF 通路。
	imageFetch *http.Client

	// sem 并发信号量。带缓冲 channel 是 Go 里最直接的做法，
	// 超出上限的投递在这里排队而不是并发挤爆上游。
	sem chan struct{}
	// inflight 防重复投递：taskID → 占位。
	inflight sync.Map

	// wg 供优雅关闭时等待在途转存完成。
	wg sync.WaitGroup
}

// newMediaArchiver 创建转存器。uploader 为 nil 表示未启用对象存储。
func newMediaArchiver(tasks *repository.MediaTaskRepo, artifacts *repository.MediaArtifactRepo,
	gateway *MediaGateway, uploader objectstore.Uploader) *mediaArchiver {
	return &mediaArchiver{
		tasks:      tasks,
		artifacts:  artifacts,
		gateway:    gateway,
		uploader:   uploader,
		imageFetch: newMediaProductClient(),
		sem:        make(chan struct{}, mediaArchiveConcurrency),
	}
}

// newMediaProductClient 回源下载产物用的客户端。
//
// 复用参考图那套 SSRF 防护（拨号时解析并固定公网 IP、重定向逐跳校验），但把
// 整体超时从参考图的 60 秒放宽到 mediaImageDownloadTimeout —— 产物单张可达
// 十几 MB，跨境慢链接需要更多时间。
func newMediaProductClient() *http.Client {
	c := newMediaRefClient()
	c.Timeout = mediaImageDownloadTimeout
	return c
}

// enabled 报告转存是否可用。
//
// 【必须校验全部依赖而不只是 uploader】转存跑在后台 goroutine 里，那里的
// nil 解引用会 panic 整个进程——没有请求边界的 recover 能接住它。装配漏了
// 任何一个依赖都该表现为「转存不可用」，而不是服务在第一个视频完成时崩掉。
func (a *mediaArchiver) enabled() bool {
	return a != nil && a.uploader != nil && a.tasks != nil && a.artifacts != nil
}

// videoArchivable 报告视频转存的依赖是否齐备。图片转存不需要 gateway
// （字节已在手上），视频需要它去取产物流。
func (a *mediaArchiver) videoArchivable() bool {
	return a.enabled() && a.gateway != nil
}

// Enqueue 投递一个视频任务的转存。非阻塞：真正的传输在后台进行。
//
// apiKey 是明文的用户 key —— 取视频产物需要它。它随闭包被后台 goroutine 持有，
// 生命周期止于本次转存结束，不落库不进日志。
func (a *mediaArchiver) Enqueue(taskID int64, apiKey, requestID string) {
	if !a.videoArchivable() || requestID == "" {
		return
	}
	// 同一任务已在途就跳过。列表每 5 秒刷新一次，storage_status 在转存完成前
	// 一直是 pending，不去重的话同一个视频会被重复下载上传好几遍。
	if _, loaded := a.inflight.LoadOrStore(taskID, struct{}{}); loaded {
		return
	}

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer a.inflight.Delete(taskID)
		// 【后台 goroutine 必须自己兜住 panic】这里没有 gin 的 recover 中间件，
		// 一次 nil 解引用就会带走整个进程——而转存失败本该只是一条日志。
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[media] 任务 %d 转存 panic: %v", taskID, r)
			}
		}()

		a.sem <- struct{}{}
		defer func() { <-a.sem }()

		// 【必须用 Background 而非请求 ctx】转存要活过发起它的那次 HTTP 请求；
		// 挂在请求 ctx 上会在响应写完的瞬间被 cancel，表现为「转存总是失败」
		// 且日志里只有一句 context canceled。
		ctx, cancel := context.WithTimeout(context.Background(), mediaArchiveTimeout)
		defer cancel()

		if err := a.archiveVideo(ctx, taskID, apiKey, requestID); err != nil {
			log.Printf("[media] 任务 %d 视频转存失败: %v", taskID, err)
			if e := a.tasks.SetStorageStatus(ctx, taskID, repository.MediaStorageFailed); e != nil {
				log.Printf("[media] 任务 %d 转存状态落库失败: %v", taskID, e)
			}
		}
	}()
}

// archiveVideo 下载视频产物并流式上传到对象存储。
func (a *mediaArchiver) archiveVideo(ctx context.Context, taskID int64, apiKey, requestID string) error {
	body, contentType, err := a.gateway.OpenVideoContent(ctx, apiKey, requestID)
	if err != nil {
		return fmt.Errorf("取上游产物失败: %w", err)
	}
	defer func() { _ = body.Close() }()

	key := mediaObjectKey(taskID, 0, extensionFor(contentType, ".mp4"))
	// size 传 -1：上游不一定给 Content-Length，而 R2 的 PutObject 需要确定长度。
	// 这意味着视频必须先缓冲——但缓冲在这里是有界的（上游产物最长 15 秒），
	// 且转存并发已限制为 2，最坏情况的内存占用可预期。
	data, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("读取上游产物失败: %w", err)
	}
	if len(data) == 0 {
		return fmt.Errorf("上游产物为空")
	}

	reader, size := objectstore.BytesBody(data)
	url, err := a.uploader.Put(ctx, key, reader, size, contentType)
	if err != nil {
		return err
	}

	if err := a.artifacts.Save(ctx, repository.MediaArtifact{
		TaskID: taskID, Index: 0, URL: url, ObjectKey: key,
		MimeType: contentType, Bytes: size,
	}); err != nil {
		return fmt.Errorf("产物记录落库失败: %w", err)
	}
	return a.tasks.SetStorageStatus(ctx, taskID, repository.MediaStorageStored)
}

// ArchiveImages 同步转存图片产物，返回是否全部成功。
//
// 【失败不让任务失败】钱已经扣了、图也拿到了，转存失败只该降级成「本次会话内
// 可见但刷新后丢失」，而不是把一次成功的生成标成失败。调用方据返回值决定是否
// 把字节仍旧 inline 返回给前端。
//
// 【b64 与 URL 都要认】上游并不保证按 response_format=b64_json 回图：实测同
// 一把 key、同一个模型，有时回 b64_json，有时只回一个直链 url（2026-10-20 的
// 线上任务 551 就是后者）。只认 b64 会把「上游其实把图给全了」误判成「没有
// 字节可转存」——用户付了钱、图也生成好了，刷新后却看不到。b64 缺失时回源
// 下载那个 url，再走同一条上传路径。
func (a *mediaArchiver) ArchiveImages(ctx context.Context, taskID int64, results []ImageResult) bool {
	if !a.enabled() {
		return false
	}

	saved := 0
	for i, r := range results {
		if r.B64 == "" && r.URL == "" {
			continue
		}
		data, mimeType, err := a.imageBytes(ctx, r)
		if err != nil {
			log.Printf("[media] 任务 %d 第 %d 张图取字节失败: %v", taskID, i, err)
			continue
		}
		key := mediaObjectKey(taskID, i, extensionFor(mimeType, ".jpg"))
		url, err := a.putImage(ctx, key, data, mimeType)
		if err != nil {
			log.Printf("[media] 任务 %d 第 %d 张图转存失败: %v", taskID, i, err)
			continue
		}
		if err := a.saveArtifact(ctx, repository.MediaArtifact{
			TaskID: taskID, Index: i, URL: url, ObjectKey: key,
			MimeType: mimeType, Bytes: int64(len(data)),
		}); err != nil {
			// 对象已传上去但记录没落库：重试也没救回来时只能标 failed，
			// 代价是一个待桶生命周期清理的孤儿对象 + 本次会话 inline 兜底。
			log.Printf("[media] 任务 %d 第 %d 张图记录落库失败: %v", taskID, i, err)
			continue
		}
		saved++
	}

	status := repository.MediaStorageStored
	if saved == 0 || saved < countArchivableImages(results) {
		// 部分成功也标 failed：前端据此保留 inline 兜底，让缺的那几张仍然可见。
		// 标 stored 会让前端只显示已转存的那部分，用户以为少生成了几张。
		status = repository.MediaStorageFailed
	}
	if err := a.tasks.SetStorageStatus(ctx, taskID, status); err != nil {
		log.Printf("[media] 任务 %d 转存状态落库失败: %v", taskID, err)
	}
	return status == repository.MediaStorageStored
}

// putImage 上传一张图片产物，瞬时失败重试一次。
//
// 每次尝试单独限时（mediaImagePutTimeout）：共享 r2 客户端那个 10 分钟总超时
// 是给视频留的，挂在一张几百 KB 的图上只会拖住整个同步提交请求。
func (a *mediaArchiver) putImage(ctx context.Context, key string, data []byte, mimeType string) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= mediaArchiveAttempts; attempt++ {
		putCtx, cancel := context.WithTimeout(ctx, mediaImagePutTimeout)
		reader, size := objectstore.BytesBody(data)
		url, err := a.uploader.Put(putCtx, key, reader, size, mimeType)
		cancel()
		if err == nil {
			if attempt > 1 {
				log.Printf("[media] 对象 %s 第 %d 次上传成功", key, attempt)
			}
			return url, nil
		}
		lastErr = err
		if attempt < mediaArchiveAttempts {
			select {
			case <-ctx.Done():
				return "", lastErr
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
	return "", lastErr
}

// saveArtifact 落产物记录，瞬时失败重试一次。
//
// Save 是 (task_id, idx) UPSERT，重试幂等。不重试的话，「对象传上去了但记录
// 没落库」会留下一个孤儿对象，而用户看到的仍是「未能转存」。
func (a *mediaArchiver) saveArtifact(ctx context.Context, art repository.MediaArtifact) error {
	var lastErr error
	for attempt := 1; attempt <= mediaArchiveAttempts; attempt++ {
		if err := a.artifacts.Save(ctx, art); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt < mediaArchiveAttempts {
			select {
			case <-ctx.Done():
				return lastErr
			case <-time.After(300 * time.Millisecond):
			}
		}
	}
	return lastErr
}

// imageBytes 取一张产物的字节与 MIME：优先用响应内联的 b64，缺失时回源下载 url。
func (a *mediaArchiver) imageBytes(ctx context.Context, r ImageResult) ([]byte, string, error) {
	if r.B64 != "" {
		data, err := base64.StdEncoding.DecodeString(r.B64)
		if err == nil && len(data) > 0 {
			// 以文件头为准判定类型：上游多半不带 mime_type，parseImagesResponse
			// 的缺省值是 image/jpeg，而 gpt-image 实际出的是 PNG。标错会让 R2
			// 返回错误的 Content-Type。判不出来时回落到上游声明值。
			mimeType := r.MimeType
			if sniffed, ok := normalizeImageType(http.DetectContentType(data)); ok {
				mimeType = sniffed
			}
			if mimeType == "" {
				mimeType = "image/jpeg"
			}
			return data, mimeType, nil
		}
		// b64 损坏但上游还给了直链时，直链仍能把图救回来——解码失败不该直接
		// 判失败。两者都不可用才报错，且把 b64 侧的原因一并带进日志。
		if r.URL == "" {
			if err != nil {
				return nil, "", fmt.Errorf("base64 解码失败: %w", err)
			}
			return nil, "", fmt.Errorf("base64 内容为空")
		}
		data, mimeType, dlErr := a.downloadProductImage(ctx, r.URL)
		if dlErr != nil {
			reason := "base64 内容为空"
			if err != nil {
				reason = "base64 解码失败: " + err.Error()
			}
			return nil, "", fmt.Errorf("%s；回源直链也失败: %w", reason, dlErr)
		}
		return data, mimeType, nil
	}
	if r.URL == "" {
		return nil, "", fmt.Errorf("上游既未返回 b64_json 也未返回 url")
	}
	return a.downloadProductImage(ctx, r.URL)
}

// downloadProductImage 回源下载上游图片直链。
//
// 【为什么能信任这个 URL 去下载】它是网关响应体里的产物地址，不是用户输入；
// 但仍按不可信处理（SSRF）：先做形态校验，真正的公网 IP 约束由 imageFetch
// 的拨号钩子兜底（见 newMediaRefClient）。
func (a *mediaArchiver) downloadProductImage(ctx context.Context, rawURL string) ([]byte, string, error) {
	if a.imageFetch == nil {
		return nil, "", fmt.Errorf("未配置产物下载客户端")
	}
	rawURL = strings.TrimSpace(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", fmt.Errorf("产物 url 无效")
	}
	if err := validateMediaRefURL(u); err != nil {
		return nil, "", fmt.Errorf("产物 url 不可下载: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, mediaImageDownloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("产物 url 无效")
	}
	// 部分直链前置 WAF 会拦非浏览器 UA（与参考图下载同一纪律）。
	// 绝不带网关 Authorization：产物直链不该拿到用户的 key。
	req.Header.Set("User-Agent", browserUA)

	resp, err := a.imageFetch.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("下载上游产物失败: %s", redactProductURL(err.Error(), rawURL))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("下载上游产物失败（HTTP %d）", resp.StatusCode)
	}
	if resp.ContentLength > mediaImageDownloadMaxBytes {
		return nil, "", fmt.Errorf("上游产物超过 %dMB", mediaImageDownloadMaxBytes>>20)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, mediaImageDownloadMaxBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("读取上游产物失败: %s", redactProductURL(err.Error(), rawURL))
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("上游产物为空")
	}
	if len(data) > mediaImageDownloadMaxBytes {
		return nil, "", fmt.Errorf("上游产物超过 %dMB", mediaImageDownloadMaxBytes>>20)
	}

	// 以文件头为准：Content-Type 可能缺省或标错，而落 R2 后浏览器按它渲染，
	// 标错会得到一个「下载」而不是可预览的图。
	mimeType, ok := normalizeImageType(http.DetectContentType(data))
	if !ok {
		return nil, "", fmt.Errorf("上游产物不是受支持的图片类型（%s）", http.DetectContentType(data))
	}
	return data, mimeType, nil
}

// redactProductURL 把日志里的产物直链抹成占位符。
//
// 直链可能带签名查询串，进日志等于把取图凭据留在日志里。错误信息里只需要
// 知道「下载失败」，不需要知道具体是哪个地址。
func redactProductURL(msg, rawURL string) string {
	if rawURL != "" {
		msg = strings.ReplaceAll(msg, rawURL, "<产物url>")
	}
	return truncate(msg, 300)
}

// countArchivableImages 统计「有字节可取」的结果数。
//
// 有 b64 或有 url 都算可取：后者由 downloadProductImage 回源。它是判断
// 「是否全部转存成功」的分母，算错会让部分失败被当成全成功。
func countArchivableImages(results []ImageResult) int {
	n := 0
	for _, r := range results {
		if r.B64 != "" || r.URL != "" {
			n++
		}
	}
	return n
}

// ResumePending 补投进程重启时遗留的在途转存。
//
// 转存中途重启会留下一批 storage_status='pending' 的孤儿：它们既不会自愈，
// 也不会被任何用户操作触发重试。启动时捞一遍是唯一的出路。
//
// resolveKey 由调用方提供：转存需要明文 key，而 archiver 不该知道 key 怎么查。
func (a *mediaArchiver) ResumePending(ctx context.Context, resolveKey func(task repository.MediaTask) string) {
	if !a.videoArchivable() {
		return
	}
	list, err := a.tasks.ListPendingStorage(ctx, 50)
	if err != nil {
		log.Printf("[media] 补扫在途转存失败: %v", err)
		return
	}
	for _, t := range list {
		key := resolveKey(t)
		if key == "" {
			// 查不到 key（用户删了它）：转存无从进行，标失败而不是永远 pending
			_ = a.tasks.SetStorageStatus(ctx, t.ID, repository.MediaStorageFailed)
			continue
		}
		a.Enqueue(t.ID, key, t.UpstreamRequestID)
	}
	if len(list) > 0 {
		log.Printf("[media] 补投 %d 条在途转存", len(list))
	}
}

// Wait 等待在途转存结束（优雅关闭用）。
func (a *mediaArchiver) Wait() {
	if a == nil {
		return
	}
	a.wg.Wait()
}

// mediaObjectKey 生成对象键：media/{yyyy}/{mm}/{taskID}/{index}{ext}。
//
// 按月分片是为了将来能给桶配「N 个月前的对象自动删除」的生命周期规则——
// 平铺在一个前缀下时，这类规则只能全删或全留。
func mediaObjectKey(taskID int64, index int, ext string) string {
	now := time.Now().UTC()
	return fmt.Sprintf("media/%04d/%02d/%d/%d%s", now.Year(), int(now.Month()), taskID, index, ext)
}

// refObjectKey 参考图对象键：media/refs/{yyyy}/{mm}/{id}{ext}。
//
// 与产物键分开：参考图在任务落库之前上传，没有 taskID；也方便以后单独配
// 生命周期（参考图可以比产物更早清）。
func refObjectKey(id, ext string) string {
	now := time.Now().UTC()
	return fmt.Sprintf("media/refs/%04d/%02d/%s%s", now.Year(), int(now.Month()), id, ext)
}

// extensionFor 从 MIME 类型推断文件扩展名，未知时用 fallback。
//
// 扩展名不影响可用性（浏览器按 Content-Type 渲染），但影响用户下载后的体验：
// 一个没有扩展名的文件在 Windows 上双击打不开。
func extensionFor(mimeType, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0])) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	default:
		return fallback
	}
}
