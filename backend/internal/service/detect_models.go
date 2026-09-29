package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"sub2api-account-monitor/internal/repository"
)

// settingsKeyDetectModels 模型检测页自定义模型清单在 settings 表里的 key。
const settingsKeyDetectModels = "detect_models"

const (
	// maxCustomDetectModels 自定义模型的数量上限：这是常用列表，不是模型库。
	maxCustomDetectModels = 50
	// maxDetectModelNameLen 单个模型名的长度上限。
	maxDetectModelNameLen = 128
)

// detectModelNameRe 模型名的字符白名单。覆盖 claude-opus-5-5、global.anthropic.claude-opus-5、
// anthropic/claude-sonnet-5、claude-opus-4-5@20251101、...-v1:0 这类写法；空白与引号一律不收。
var detectModelNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]*$`)

// ErrInvalidDetectModel 模型名不合法或清单已满（请求方的问题，handler 据此回 400）。
var ErrInvalidDetectModel = errors.New("自定义模型不合法")

// NormalizeDetectModel 去掉首尾空白后校验模型名。
func NormalizeDetectModel(name string) (string, error) {
	model := strings.TrimSpace(name)
	switch {
	case model == "":
		return "", fmt.Errorf("%w：模型名不能为空", ErrInvalidDetectModel)
	case len(model) > maxDetectModelNameLen:
		return "", fmt.Errorf("%w：模型名不能超过 %d 个字符", ErrInvalidDetectModel, maxDetectModelNameLen)
	case !detectModelNameRe.MatchString(model):
		return "", fmt.Errorf("%w：%q 只能包含字母、数字和 . _ : / @ + -", ErrInvalidDetectModel, model)
	}
	return model, nil
}

// withModel 返回追加了 model 的新清单；已存在时原样返回，不重复添加。
func withModel(list []string, model string) ([]string, error) {
	for _, m := range list {
		if m == model {
			return list, nil
		}
	}
	if len(list) >= maxCustomDetectModels {
		return nil, fmt.Errorf("%w：自定义模型最多 %d 个，请先移除不用的", ErrInvalidDetectModel, maxCustomDetectModels)
	}
	out := make([]string, 0, len(list)+1)
	out = append(out, list...)
	return append(out, model), nil
}

// withoutModel 返回去掉 model 的新清单；不存在时原样返回。
func withoutModel(list []string, model string) []string {
	out := make([]string, 0, len(list))
	for _, m := range list {
		if m != model {
			out = append(out, m)
		}
	}
	return out
}

// DetectModelStore 模型检测页的自定义模型清单。
//
// 全站共享（存 settings 表）：一个人加上的模型，其他人打开页面就能一键选用。
// 输入框本身仍可临时填任意模型名，不必先保存。
type DetectModelStore struct {
	repo *repository.SettingsRepo
	// mu 串行化「读 → 改 → 写」，两个人同时增删也不会互相覆盖。
	mu sync.Mutex
}

// NewDetectModelStore 创建 DetectModelStore。
func NewDetectModelStore(repo *repository.SettingsRepo) *DetectModelStore {
	return &DetectModelStore{repo: repo}
}

// List 当前自定义模型，按添加顺序。
func (s *DetectModelStore) List(ctx context.Context) ([]string, error) {
	raw, err := s.repo.Get(ctx, settingsKeyDetectModels)
	if err != nil {
		return nil, fmt.Errorf("读取自定义模型失败: %w", err)
	}
	models := []string{}
	if raw == "" {
		return models, nil
	}
	if err := json.Unmarshal([]byte(raw), &models); err != nil {
		return nil, fmt.Errorf("解析自定义模型失败: %w", err)
	}
	if models == nil {
		// 库里存的是 null 时也按空清单返回，前端恒拿到数组
		models = []string{}
	}
	return models, nil
}

// Add 把模型加入清单并返回最新清单；已存在时不重复添加。
func (s *DetectModelStore) Add(ctx context.Context, name string) ([]string, error) {
	model, err := NormalizeDetectModel(name)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	next, err := withModel(current, model)
	if err != nil {
		return nil, err
	}
	if len(next) == len(current) {
		return current, nil
	}
	if err := s.save(ctx, next); err != nil {
		return nil, err
	}
	return next, nil
}

// Remove 把模型移出清单并返回最新清单；不在清单里时原样返回。
func (s *DetectModelStore) Remove(ctx context.Context, name string) ([]string, error) {
	model := strings.TrimSpace(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	next := withoutModel(current, model)
	if len(next) == len(current) {
		return current, nil
	}
	if err := s.save(ctx, next); err != nil {
		return nil, err
	}
	return next, nil
}

func (s *DetectModelStore) save(ctx context.Context, models []string) error {
	raw, err := json.Marshal(models)
	if err != nil {
		return fmt.Errorf("序列化自定义模型失败: %w", err)
	}
	if err := s.repo.Set(ctx, settingsKeyDetectModels, string(raw)); err != nil {
		return fmt.Errorf("保存自定义模型失败: %w", err)
	}
	return nil
}
