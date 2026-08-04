package handler

import (
	"time"

	"sub2api-account-monitor/internal/config"

	"github.com/gin-gonic/gin"
)

// resolveDayRange 解析 start/end 查询参数（YYYY-MM-DD，按配置时区），缺省为今日。
// 返回 UTC 左闭右开区间：[start 日 0 点, end 日次日 0 点)。
func resolveDayRange(c *gin.Context, cfg *config.Config) (time.Time, time.Time) {
	start, end := cfg.TodayRange()
	if s := c.Query("start"); s != "" {
		if t, err := time.ParseInLocation("2006-01-02", s, cfg.Location); err == nil {
			start, _ = cfg.DayRange(t)
		}
	}
	if e := c.Query("end"); e != "" {
		if t, err := time.ParseInLocation("2006-01-02", e, cfg.Location); err == nil {
			_, end = cfg.DayRange(t)
		}
	}
	return start, end
}

// dateBounds 把左闭右开区间转为闭区间日期串（YYYY-MM-DD）。
// 上游成本按天存储、按闭区间查询，故 end 回退一秒取其所在日期。
func dateBounds(cfg *config.Config, start, end time.Time) (string, string) {
	return start.In(cfg.Location).Format("2006-01-02"),
		end.In(cfg.Location).Add(-time.Second).Format("2006-01-02")
}
