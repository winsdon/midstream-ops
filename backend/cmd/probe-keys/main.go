// Command probe-keys 一次性探查：验证上游 sub2api 站点 API key 明文能否与本站 accounts.credentials->>'api_key' 精确匹配。
// 仅用于口径核对，不修改任何数据。
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"sub2api-account-monitor/internal/config"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.Sub2api.DSN())
	if err != nil {
		log.Fatalf("PG 连接失败: %v", err)
	}
	defer pool.Close()

	// 本站账号：api_key 指纹 -> 账号信息
	rows, err := pool.Query(ctx, `
		SELECT id, COALESCE(name,''), COALESCE(platform,''),
		       COALESCE(rate_multiplier,1),
		       COALESCE(credentials->>'api_key',''),
		       COALESCE(NULLIF(credentials->>'base_url',''), extra->>'base_url', '')
		FROM accounts WHERE deleted_at IS NULL`)
	if err != nil {
		log.Fatalf("查询 accounts 失败: %v", err)
	}
	type acc struct {
		id       int64
		name     string
		platform string
		rate     float64
		keyHash  string
		keyLen   int
		baseURL  string
	}
	byHash := map[string]acc{}
	var all []acc
	for rows.Next() {
		var a acc
		var key string
		if err := rows.Scan(&a.id, &a.name, &a.platform, &a.rate, &key, &a.baseURL); err != nil {
			log.Fatalf("scan 失败: %v", err)
		}
		a.keyLen = len(key)
		if key != "" {
			h := sha256.Sum256([]byte(strings.TrimSpace(key)))
			a.keyHash = hex.EncodeToString(h[:])
			byHash[a.keyHash] = a
		}
		all = append(all, a)
	}
	rows.Close()
	fmt.Printf("本站账号总数=%d 其中有 api_key 的=%d\n", len(all), len(byHash))

	// 上游站点 key 列表（从环境变量取凭据，避免硬编码）
	baseURL := os.Getenv("PROBE_BASE_URL")
	email := os.Getenv("PROBE_EMAIL")
	password := os.Getenv("PROBE_PASSWORD")
	if baseURL == "" || email == "" || password == "" {
		log.Fatal("需要 PROBE_BASE_URL / PROBE_EMAIL / PROBE_PASSWORD 环境变量")
	}

	upstream, err := fetchUpstreamKeys(ctx, baseURL, email, password)
	if err != nil {
		log.Fatalf("拉取上游 key 列表失败: %v", err)
	}
	fmt.Printf("上游 key 数=%d\n\n", len(upstream))

	matched, unmatched := 0, 0
	matchedIDs := map[int64]acc{} // 上游 keyID -> 本站账号
	for _, uk := range upstream {
		h := sha256.Sum256([]byte(strings.TrimSpace(uk.Key)))
		hs := hex.EncodeToString(h[:])
		if a, ok := byHash[hs]; ok {
			matched++
			matchedIDs[uk.ID] = a
			fmt.Printf("[MATCH ] 上游 id=%-5d %-14q keylen=%d  ->  本站 id=%-4d %q rate=%.4f\n",
				uk.ID, uk.Name, len(uk.Key), a.id, a.name, a.rate)
		} else {
			unmatched++
			fmt.Printf("[NOMATCH] 上游 id=%-5d %-14q keylen=%d 前缀=%s\n",
				uk.ID, uk.Name, len(uk.Key), safePrefix(uk.Key))
		}
	}
	fmt.Printf("\n匹配=%d 未匹配=%d\n", matched, unmatched)

	// 本站账号 key 长度分布（判断存的是否同格式）
	lenDist := map[int]int{}
	for _, a := range all {
		lenDist[a.keyLen]++
	}
	fmt.Printf("本站 api_key 长度分布: %v\n", lenDist)

	// ---- 对账：上游真实实扣 vs 本站旧口径估算 ----
	start, end := cfg.TodayRange()
	fmt.Printf("\n=== 今日对账 [%s, %s) ===\n", start.Format(time.RFC3339), end.Format(time.RFC3339))

	usageRows, err := pool.Query(ctx, `
		SELECT account_id,
		       COUNT(*),
		       COALESCE(SUM(total_cost),0),
		       COALESCE(SUM(total_cost * COALESCE(account_rate_multiplier,1)),0),
		       COALESCE(SUM(actual_cost),0),
		       COALESCE(MIN(account_rate_multiplier),0),
		       COALESCE(MAX(account_rate_multiplier),0)
		FROM usage_logs
		WHERE created_at >= $1 AND created_at < $2
		GROUP BY 1`, start, end)
	if err != nil {
		log.Fatalf("查询 usage_logs 失败: %v", err)
	}
	type usage struct {
		requests         int64
		totalCost        float64
		oldCost          float64
		revenue          float64
		rateMin, rateMax float64
	}
	byAcct := map[int64]usage{}
	for usageRows.Next() {
		var id int64
		var u usage
		if err := usageRows.Scan(&id, &u.requests, &u.totalCost, &u.oldCost, &u.revenue, &u.rateMin, &u.rateMax); err != nil {
			log.Fatalf("scan usage 失败: %v", err)
		}
		byAcct[id] = u
	}
	usageRows.Close()

	upUsage, err := fetchUpstreamUsage(ctx, baseURL, email, password, upstream)
	if err != nil {
		log.Fatalf("拉取上游用量失败: %v", err)
	}

	fmt.Printf("%-22s %9s %12s %12s %12s %12s\n", "账号", "请求", "上游实扣", "旧口径成本", "官价", "本站收益")
	var sumUp, sumOld, sumRev float64
	for _, uk := range upstream {
		a, ok := matchedIDs[uk.ID]
		if !ok {
			continue
		}
		u := byAcct[a.id]
		up := upUsage[uk.ID]
		sumUp += up
		sumOld += u.oldCost
		sumRev += u.revenue
		fmt.Printf("%-22s %9d %12.4f %12.4f %12.4f %12.4f  rate[%.3f~%.3f]\n",
			trunc(a.name, 22), u.requests, up, u.oldCost, u.totalCost, u.revenue, u.rateMin, u.rateMax)
	}
	fmt.Printf("%-22s %9s %12.4f %12.4f %12s %12.4f\n", "合计(哈基米)", "", sumUp, sumOld, "", sumRev)
	fmt.Printf("\n真实利润=%.4f  旧口径利润=%.4f  差额=%.4f\n", sumRev-sumUp, sumRev-sumOld, sumOld-sumUp)
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func safePrefix(k string) string {
	if len(k) < 8 {
		return "***"
	}
	return k[:8] + "***"
}

type upstreamKey struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Key  string `json:"key"`
}

func fetchUpstreamKeys(ctx context.Context, baseURL, email, password string) ([]upstreamKey, error) {
	c := &http.Client{Timeout: 30 * time.Second}
	base := strings.TrimRight(baseURL, "/")

	// login
	lb, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/auth/login", bytes.NewReader(lb))
	req.Header.Set("Content-Type", "application/json")
	setUA(req)
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	var lenv struct {
		Code int `json:"code"`
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&lenv)
	resp.Body.Close()
	if lenv.Data.AccessToken == "" {
		return nil, fmt.Errorf("登录未取到 token")
	}

	// keys
	req2, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/keys?page=1&page_size=100", nil)
	req2.Header.Set("Authorization", "Bearer "+lenv.Data.AccessToken)
	setUA(req2)
	resp2, err := c.Do(req2)
	if err != nil {
		return nil, err
	}
	defer resp2.Body.Close()
	var kenv struct {
		Code int `json:"code"`
		Data struct {
			Items []upstreamKey `json:"items"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&kenv); err != nil {
		return nil, err
	}
	return kenv.Data.Items, nil
}

func setUA(r *http.Request) {
	r.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36")
	r.Header.Set("Accept", "application/json, text/plain, */*")
}

// fetchUpstreamUsage 拉取上游 per-key 今日实扣（keyID -> today_actual_cost）。
func fetchUpstreamUsage(ctx context.Context, baseURL, email, password string, keys []upstreamKey) (map[int64]float64, error) {
	c := &http.Client{Timeout: 30 * time.Second}
	base := strings.TrimRight(baseURL, "/")

	lb, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/auth/login", bytes.NewReader(lb))
	req.Header.Set("Content-Type", "application/json")
	setUA(req)
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	var lenv struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&lenv)
	resp.Body.Close()

	ids := make([]int64, 0, len(keys))
	for _, k := range keys {
		ids = append(ids, k.ID)
	}
	ub, _ := json.Marshal(map[string]any{"api_key_ids": ids})
	req2, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/usage/dashboard/api-keys-usage", bytes.NewReader(ub))
	req2.Header.Set("Authorization", "Bearer "+lenv.Data.AccessToken)
	req2.Header.Set("Content-Type", "application/json")
	setUA(req2)
	resp2, err := c.Do(req2)
	if err != nil {
		return nil, err
	}
	defer resp2.Body.Close()
	var uenv struct {
		Data struct {
			Stats map[string]struct {
				APIKeyID        int64   `json:"api_key_id"`
				TodayActualCost float64 `json:"today_actual_cost"`
			} `json:"stats"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&uenv); err != nil {
		return nil, err
	}
	out := map[int64]float64{}
	for _, s := range uenv.Data.Stats {
		out[s.APIKeyID] = s.TodayActualCost
	}
	return out, nil
}
