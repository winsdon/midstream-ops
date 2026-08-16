// Package objectstore 提供对象存储上传能力。
//
// 【为什么本站需要它】生成的图片走 b64 从网关取回、视频只有一个带认证的临时端点，
// 两者都无法直接交给浏览器长期使用：刷新页面图片就没了，视频在上游过保留期后
// 连后端也取不到。把产物转存到对象存储后，前端拿到的是一个不需要认证、不会过期
// 的普通 URL。
package objectstore

import (
	"context"
	"io"
)

// Uploader 对象存储上传器。
//
// 【接口只有一个方法】本站的需求就是「把字节放上去，拿回一个能公开访问的 URL」。
// 不定义 Get/Delete：读取由浏览器直连公开域名完成，删除交给桶的生命周期规则——
// 让「删任务记录」这个高频操作依赖一次外部网络调用是不划算的。
//
// 按 Go 的惯例定义在使用方（本包被 service 依赖）而非实现方。
type Uploader interface {
	// Put 上传一个对象并返回其公开访问 URL。
	//
	// size 为 -1 表示长度未知（流式上传）。实现可据此选择是否走分块编码。
	Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) (string, error)
}
