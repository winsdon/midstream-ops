package service

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// EmbedSession 一次 iframe 嵌入会话的身份快照。
//
// 刻意不保存 sub2api token：token 只在换会话那一次用于回调 /auth/me，
// 之后由 monitor 自签的 sessionToken 承担后续请求的身份。
type EmbedSession struct {
	UserID    string
	Email     string
	ExpiresAt time.Time
}

// EmbedSessionStore 进程内嵌入会话存储。
//
// monitor 是单二进制单实例部署，用内存 map 即可，不引入 Redis。进程重启后会话
// 失效属可接受行为——用户刷新 iframe 会重新换一次会话。
type EmbedSessionStore struct {
	mu       sync.RWMutex
	sessions map[string]EmbedSession
	ttl      time.Duration
	stop     chan struct{}
}

// embedSessionSweepInterval 后台清理间隔。
const embedSessionSweepInterval = 5 * time.Minute

// NewEmbedSessionStore 创建会话存储并启动后台清理。ttl <= 0 时回退 30 分钟。
func NewEmbedSessionStore(ttl time.Duration) *EmbedSessionStore {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	s := &EmbedSessionStore{
		sessions: make(map[string]EmbedSession),
		ttl:      ttl,
		stop:     make(chan struct{}),
	}
	go s.sweepLoop()
	return s
}

// Create 为已校验的身份签发会话，返回 token 与有效期（秒）。
func (s *EmbedSessionStore) Create(userID, email string) (string, int, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", 0, err
	}
	token := hex.EncodeToString(buf)

	s.mu.Lock()
	s.sessions[token] = EmbedSession{
		UserID:    userID,
		Email:     email,
		ExpiresAt: time.Now().Add(s.ttl),
	}
	s.mu.Unlock()

	return token, int(s.ttl.Seconds()), nil
}

// Get 查询会话；不存在或已过期返回 false（过期项顺手删除）。
func (s *EmbedSessionStore) Get(token string) (EmbedSession, bool) {
	if token == "" {
		return EmbedSession{}, false
	}
	s.mu.RLock()
	sess, ok := s.sessions[token]
	s.mu.RUnlock()
	if !ok {
		return EmbedSession{}, false
	}
	if time.Now().After(sess.ExpiresAt) {
		s.mu.Lock()
		delete(s.sessions, token)
		s.mu.Unlock()
		return EmbedSession{}, false
	}
	return sess, true
}

// Close 停止后台清理协程。
func (s *EmbedSessionStore) Close() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
}

func (s *EmbedSessionStore) sweepLoop() {
	ticker := time.NewTicker(embedSessionSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.sweep()
		}
	}
}

func (s *EmbedSessionStore) sweep() {
	now := time.Now()
	s.mu.Lock()
	for token, sess := range s.sessions {
		if now.After(sess.ExpiresAt) {
			delete(s.sessions, token)
		}
	}
	s.mu.Unlock()
}
