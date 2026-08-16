package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
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
		tasks:     tasks,
		artifacts: artifacts,
		gateway:   gateway,
		uploader:  uploader,
		sem:       make(chan struct{}, mediaArchiveConcurrency),
	}
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
// 把 b64 仍旧 inline 返回给前端。
func (a *mediaArchiver) ArchiveImages(ctx context.Context, taskID int64, results []ImageResult) bool {
	if !a.enabled() {
		return false
	}

	saved := 0
	for i, r := range results {
		if r.B64 == "" {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(r.B64)
		if err != nil {
			log.Printf("[media] 任务 %d 第 %d 张图 base64 解码失败: %v", taskID, i, err)
			continue
		}
		key := mediaObjectKey(taskID, i, extensionFor(r.MimeType, ".jpg"))
		reader, size := objectstore.BytesBody(data)
		url, err := a.uploader.Put(ctx, key, reader, size, r.MimeType)
		if err != nil {
			log.Printf("[media] 任务 %d 第 %d 张图转存失败: %v", taskID, i, err)
			continue
		}
		if err := a.artifacts.Save(ctx, repository.MediaArtifact{
			TaskID: taskID, Index: i, URL: url, ObjectKey: key,
			MimeType: r.MimeType, Bytes: size,
		}); err != nil {
			log.Printf("[media] 任务 %d 第 %d 张图记录落库失败: %v", taskID, i, err)
			continue
		}
		saved++
	}

	status := repository.MediaStorageStored
	if saved == 0 {
		status = repository.MediaStorageFailed
	} else if saved < countB64Images(results) {
		// 部分成功也标 failed：前端据此保留 inline 兜底，让缺的那几张仍然可见。
		// 标 stored 会让前端只显示已转存的部分，用户以为少生成了几张。
		status = repository.MediaStorageFailed
	}
	if err := a.tasks.SetStorageStatus(ctx, taskID, status); err != nil {
		log.Printf("[media] 任务 %d 转存状态落库失败: %v", taskID, err)
	}
	return status == repository.MediaStorageStored
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

// countB64Images 统计有 base64 内容的结果数（分母，用于判定是否全部转存成功）。
func countB64Images(results []ImageResult) int {
	n := 0
	for _, r := range results {
		if r.B64 != "" {
			n++
		}
	}
	return n
}
