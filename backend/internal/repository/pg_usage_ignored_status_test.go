package repository

import "testing"

// 这是本仓库唯一一条纯单元测试覆盖 PG SQL 参数编码的地方：
// 稳定性页默认排除 400/403/429/529 依赖 `status_code <> ALL($n::int[])`，
// 而 pgx 把 nil 切片编成 SQL NULL，`x <> ALL(NULL)` 求值为 NULL，
// 会把 error_agg 整个滤掉 —— 失败数恒 0、SLA 恒 100%，且**不报错**。
func TestNormalizeIgnoredStatusCodesNeverNil(t *testing.T) {
	if got := normalizeIgnoredStatusCodes(nil); got == nil {
		t.Fatal("nil 切片会编成 SQL NULL，误伤全部失败统计")
	} else if len(got) != 0 {
		t.Fatalf("nil 应归一化为空切片，得到 %v", got)
	}
	if got := normalizeIgnoredStatusCodes([]int32{}); got == nil || len(got) != 0 {
		t.Fatalf("空切片应原样保留，得到 %v", got)
	}
}

func TestNormalizeIgnoredStatusCodesKeepsValues(t *testing.T) {
	got := normalizeIgnoredStatusCodes([]int32{400, 403, 429, 529})
	if len(got) != 4 || got[0] != 400 || got[3] != 529 {
		t.Fatalf("正常集合被改写: %v", got)
	}
}
