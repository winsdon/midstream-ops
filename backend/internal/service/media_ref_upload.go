package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"sub2api-account-monitor/internal/pkg/objectstore"
)

// 参考图允许的 MIME。与前端 accept 对齐；其余类型上游也不认。
var allowedRefImageTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/jpg":  ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

const (
	refImageSniffBytes = 512
	refPresignTTL      = 5 * time.Minute
)

// RefUploadRequest 前端直传前向本站申请一个预签名槽。
type RefUploadRequest struct {
	Filename    string
	ContentType string
	Size        int64
}

// RefUploadSlot 浏览器 PUT 所需的地址与必须带上的头。
type RefUploadSlot struct {
	UploadURL   string
	PublicURL   string
	Headers     map[string]string
	ContentType string
}

// PrepareRefUploads 为每张参考图签发一个短时预签名 PUT。
//
// 文件字节不经过本站：前端拿 upload_url 直传 R2，再把 public_url 带回生成请求。
func (s *MediaService) PrepareRefUploads(reqs []RefUploadRequest) ([]RefUploadSlot, error) {
	if len(reqs) == 0 {
		return nil, fmt.Errorf("缺少参考图")
	}
	if len(reqs) > mediaMaxRefImages {
		return nil, fmt.Errorf("参考图最多 %d 张", mediaMaxRefImages)
	}
	if s == nil || s.uploader == nil {
		return nil, ErrMediaStorageRequired
	}
	presigner, ok := s.uploader.(objectstore.Presigner)
	if !ok {
		return nil, ErrMediaStorageRequired
	}

	out := make([]RefUploadSlot, 0, len(reqs))
	for _, req := range reqs {
		if req.Size <= 0 {
			return nil, fmt.Errorf("参考图为空")
		}
		if req.Size > maxRefImageBytes {
			return nil, fmt.Errorf("参考图超过 20MB")
		}
		contentType, ext, err := resolveRefImageType("", req.ContentType, req.Filename)
		if err != nil {
			return nil, err
		}
		key := refObjectKey(newRefID(), ext)
		uploadURL, publicURL, err := presigner.PresignPut(key, contentType, refPresignTTL)
		if err != nil {
			return nil, fmt.Errorf("签发上传地址失败: %w", err)
		}
		out = append(out, RefUploadSlot{
			UploadURL:   uploadURL,
			PublicURL:   publicURL,
			ContentType: contentType,
			Headers:     map[string]string{"Content-Type": contentType},
		})
	}
	return out, nil
}

// uploadRefImages 把本地参考图上传到对象存储，返回公开 URL 列表。
//
// 【为什么必须先上传】图生图走 multipart 时，sub2api 会把文件编成 data URL
// 再交给 xAI，上游回 400 Invalid base64-encoded image。图生视频则直接 415。
// 两边都只认公网 http(s) 的 image.url。
func (s *MediaService) uploadRefImages(ctx context.Context, files []MediaUploadFile) ([]string, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("缺少参考图")
	}
	if s == nil || s.uploader == nil {
		return nil, ErrMediaStorageRequired
	}

	urls := make([]string, 0, len(files))
	for _, f := range files {
		url, err := s.uploadOneRef(ctx, f)
		if err != nil {
			return nil, err
		}
		urls = append(urls, url)
	}
	return urls, nil
}

func (s *MediaService) uploadOneRef(ctx context.Context, f MediaUploadFile) (string, error) {
	if f.Size > maxRefImageBytes {
		return "", fmt.Errorf("参考图超过 20MB")
	}
	if f.Reader == nil {
		return "", fmt.Errorf("缺少参考图")
	}

	body, sniffType, err := sniffRefImage(f.Reader)
	if err != nil {
		return "", err
	}
	contentType, ext, err := resolveRefImageType(sniffType, f.ContentType, f.Filename)
	if err != nil {
		return "", err
	}

	size := f.Size
	if size <= 0 {
		data, err := io.ReadAll(io.LimitReader(body, maxRefImageBytes+1))
		if err != nil {
			return "", fmt.Errorf("读取参考图失败: %w", err)
		}
		if int64(len(data)) > maxRefImageBytes {
			return "", fmt.Errorf("参考图超过 20MB")
		}
		if len(data) == 0 {
			return "", fmt.Errorf("参考图为空")
		}
		body = bytes.NewReader(data)
		size = int64(len(data))
	}

	key := refObjectKey(newRefID(), ext)
	url, err := s.uploader.Put(ctx, key, body, size, contentType)
	if err != nil {
		return "", fmt.Errorf("上传参考图失败: %w", err)
	}
	if !isPublicHTTPURL(url) {
		return "", fmt.Errorf("对象存储未返回可访问地址")
	}
	return url, nil
}

// sniffRefImage 读文件头判定真实类型，再把已读字节拼回去，避免整张图进内存。
func sniffRefImage(r io.Reader) (io.Reader, string, error) {
	buf := make([]byte, refImageSniffBytes)
	n, err := io.ReadFull(r, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, "", fmt.Errorf("读取参考图失败: %w", err)
	}
	buf = buf[:n]
	if n == 0 {
		return nil, "", fmt.Errorf("参考图为空")
	}
	return io.MultiReader(bytes.NewReader(buf), r), http.DetectContentType(buf), nil
}

func resolveRefImageType(sniffed, declared, filename string) (contentType, ext string, err error) {
	if ct, ok := normalizeImageType(sniffed); ok {
		return ct, allowedRefImageTypes[ct], nil
	}
	if ct, ok := normalizeImageType(declared); ok {
		return ct, extFromName(filename, allowedRefImageTypes[ct]), nil
	}
	if ct, ok := typeFromFilename(filename); ok {
		return ct, allowedRefImageTypes[ct], nil
	}
	return "", "", ErrMediaBadImageType
}

func normalizeImageType(raw string) (string, bool) {
	ct := strings.ToLower(strings.TrimSpace(strings.Split(raw, ";")[0]))
	if ct == "image/jpg" {
		ct = "image/jpeg"
	}
	_, ok := allowedRefImageTypes[ct]
	if !ok {
		return "", false
	}
	return ct, true
}

func typeFromFilename(name string) (string, bool) {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".png":
		return "image/png", true
	case ".webp":
		return "image/webp", true
	default:
		return "", false
	}
}

func extFromName(name, fallback string) string {
	if ext := strings.ToLower(path.Ext(name)); ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" {
		if ext == ".jpeg" {
			return ".jpg"
		}
		return ext
	}
	return fallback
}

func newRefID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", bytesSeed())
	}
	return hex.EncodeToString(b[:])
}

func bytesSeed() int64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	var n int64
	for _, v := range b {
		n = n<<8 | int64(v)
	}
	if n < 0 {
		n = -n
	}
	return n
}

// maxRefImageBytes 与 handler.maxUploadBytes 对齐。
const maxRefImageBytes = 20 << 20
