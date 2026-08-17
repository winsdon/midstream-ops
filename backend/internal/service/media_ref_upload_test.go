package service

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// 最小 PNG 文件头，http.DetectContentType 能认出 image/png。
var pngHeader = []byte("\x89PNG\r\n\x1a\n" + "IHDR-not-a-real-png-but-enough-to-sniff")

func TestUploadRefImagesPutsToObjectStore(t *testing.T) {
	up := &fakeUploader{}
	s := &MediaService{uploader: up}

	urls, err := s.uploadRefImages(context.Background(), []MediaUploadFile{{
		Filename:    "ref.png",
		ContentType: "image/png",
		Size:        int64(len(pngHeader)),
		Reader:      bytes.NewReader(pngHeader),
	}})
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if len(urls) != 1 || !strings.HasPrefix(urls[0], "https://cdn.example.com/media/refs/") {
		t.Fatalf("公开 URL 不对: %v", urls)
	}
	if !strings.HasSuffix(urls[0], ".png") {
		t.Fatalf("对象键应带 .png: %s", urls[0])
	}

	calls := up.snapshot()
	if len(calls) != 1 {
		t.Fatalf("应上传 1 次，实得 %d", len(calls))
	}
	if calls[0].ContentType != "image/png" {
		t.Fatalf("Content-Type 错误: %s", calls[0].ContentType)
	}
	if calls[0].Size != int64(len(pngHeader)) {
		t.Fatalf("Size 错误: %d", calls[0].Size)
	}
	if !strings.HasPrefix(calls[0].Key, "media/refs/") {
		t.Fatalf("对象键前缀错误: %s", calls[0].Key)
	}
}

func TestUploadRefImagesRequiresStorage(t *testing.T) {
	s := &MediaService{}
	_, err := s.uploadRefImages(context.Background(), []MediaUploadFile{{
		Filename: "a.png", Size: int64(len(pngHeader)), Reader: bytes.NewReader(pngHeader),
	}})
	if !errors.Is(err, ErrMediaStorageRequired) {
		t.Fatalf("未配置对象存储应返回 ErrMediaStorageRequired，实得 %v", err)
	}
}

func TestUploadRefImagesRejectsNonImage(t *testing.T) {
	up := &fakeUploader{}
	s := &MediaService{uploader: up}

	_, err := s.uploadRefImages(context.Background(), []MediaUploadFile{{
		Filename:    "note.txt",
		ContentType: "text/plain",
		Size:        5,
		Reader:      strings.NewReader("hello"),
	}})
	if !errors.Is(err, ErrMediaBadImageType) {
		t.Fatalf("非图片应拒绝，实得 %v", err)
	}
	if n := len(up.snapshot()); n != 0 {
		t.Fatalf("拒绝后不应上传，实得 %d 次", n)
	}
}

func TestPrepareRefUploadsIssuesPresignedSlots(t *testing.T) {
	up := &fakeUploader{}
	s := &MediaService{uploader: up}

	slots, err := s.PrepareRefUploads([]RefUploadRequest{
		{Filename: "a.png", ContentType: "image/png", Size: 12},
		{Filename: "b.jpg", ContentType: "image/jpeg", Size: 34},
	})
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	if len(slots) != 2 {
		t.Fatalf("应签发 2 个槽，实得 %d", len(slots))
	}
	if !strings.HasPrefix(slots[0].UploadURL, "https://upload.example.com/media/refs/") {
		t.Fatalf("上传 URL 错误: %s", slots[0].UploadURL)
	}
	if !strings.HasPrefix(slots[0].PublicURL, "https://cdn.example.com/media/refs/") {
		t.Fatalf("公开 URL 错误: %s", slots[0].PublicURL)
	}
	if slots[0].Headers["Content-Type"] != "image/png" || slots[1].Headers["Content-Type"] != "image/jpeg" {
		t.Fatalf("Content-Type 未锁定: %+v", slots)
	}
}

func TestPrepareRefUploadsRequiresStorage(t *testing.T) {
	s := &MediaService{}
	_, err := s.PrepareRefUploads([]RefUploadRequest{{Filename: "a.png", ContentType: "image/png", Size: 1}})
	if !errors.Is(err, ErrMediaStorageRequired) {
		t.Fatalf("应返回 ErrMediaStorageRequired，实得 %v", err)
	}
}

func TestPrepareRefUploadsRejectsTooMany(t *testing.T) {
	s := &MediaService{uploader: &fakeUploader{}}
	reqs := make([]RefUploadRequest, mediaMaxRefImages+1)
	for i := range reqs {
		reqs[i] = RefUploadRequest{Filename: "a.png", ContentType: "image/png", Size: 1}
	}
	if _, err := s.PrepareRefUploads(reqs); err == nil {
		t.Fatal("超过 4 张应被拒绝")
	}
}

func TestUploadRefImagesRejectsEmpty(t *testing.T) {
	s := &MediaService{uploader: &fakeUploader{}}
	_, err := s.uploadRefImages(context.Background(), []MediaUploadFile{{
		Filename: "a.png", Size: 0, Reader: bytes.NewReader(nil),
	}})
	if err == nil {
		t.Fatal("空文件应被拒绝")
	}
}
