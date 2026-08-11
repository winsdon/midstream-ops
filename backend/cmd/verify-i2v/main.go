// 一次性验收工具：用真实的 MediaGateway 代码路径打上游，确认图生视频的参考图真正生效。
//
// 【为什么需要这个】单测只能证明「我们发出的 body 形状对」，证明不了「上游真的用了参考图」。
// 本次事故的教训正是：字段名与供应商文档一致、单测通过、HTTP 200、照常扣费，
// 产物却完全不参考图片。对扣费且无错误码的接口，验收必须落到产物本身。
//
// 用法：
//
//	go run ./cmd/verify-i2v -base https://... -key sk-... -image https://... -out ./out.mp4
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"sub2api-account-monitor/internal/service"
)

func main() {
	base := flag.String("base", "", "网关根地址，如 https://api.example.com")
	key := flag.String("key", "", "API Key")
	image := flag.String("image", "", "参考图公网地址")
	prompt := flag.String("prompt", "slow gentle camera push in, subtle motion", "提示词。刻意不描述主体，好让产物只能来自参考图")
	out := flag.String("out", "verify-i2v.mp4", "产物落盘路径")
	flag.Parse()

	if *base == "" || *key == "" || *image == "" {
		fmt.Fprintln(os.Stderr, "缺少参数：-base -key -image 均为必填")
		os.Exit(2)
	}

	g := service.NewMediaGateway(*base)
	ctx := context.Background()

	requestID, err := g.SubmitVideo(ctx, *key, service.MediaGenerateParams{
		Kind:       service.MediaKindImage2Video,
		Model:      "grok-imagine-video",
		Prompt:     *prompt,
		Resolution: "480p",
		Duration:   5,
		ImageURL:   *image,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "提交失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("已提交（已扣费）request_id=%s\n", requestID)

	// 轮询到终态。5 秒一次，与前端节奏一致。
	for i := 1; i <= 60; i++ {
		st, err := g.QueryVideo(ctx, *key, requestID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "查询失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("[%02d] done=%v progress=%d\n", i, st.Done, st.Progress)
		if st.Done {
			break
		}
		time.Sleep(5 * time.Second)
	}

	body, ct, err := g.OpenVideoContent(ctx, *key, requestID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "取产物失败: %v\n", err)
		os.Exit(1)
	}
	defer body.Close()

	f, err := os.Create(*out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建文件失败: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	n, err := io.Copy(f, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "写入失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("已保存 %s（%s，%d 字节）\n", *out, ct, n)
	fmt.Println("请肉眼比对首帧与参考图 —— 这是唯一可信的验收标准。")
}
