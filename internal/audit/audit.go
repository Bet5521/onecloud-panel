package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"onecloud-panel/internal/auth"
	"onecloud-panel/internal/store"
)

type ctxKey int

const requestIDKey ctxKey = 1

// Result 常量。
const (
	ResultSuccess = "success"
	ResultFailure = "failure"
)

// Service 审计服务。
type Service struct {
	store *store.Store
}

// New 创建审计服务。
func New(s *store.Store) *Service { return &Service{store: s} }

// Record 写入一条审计记录，从请求上下文提取身份、IP、请求 ID。
func (s *Service) Record(r *http.Request, module, action, targetType, targetID, result, detail string) {
	e := store.AuditLog{
		Ts:         time.Now().Unix(),
		IP:         auth.ClientIP(r),
		Module:     module,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Result:     result,
		RequestID:  RequestIDFrom(r.Context()),
		Detail:     detail,
	}
	if id := auth.FromContext(r.Context()); id != nil && id.User != nil {
		uid := id.User.ID
		e.UserID = &uid
		e.Username = id.User.Username
	}
	if err := s.store.InsertAuditLog(&e); err != nil {
		log.Printf("审计记录写入失败（%s/%s）: %v", e.Module, e.Action, err)
	}
}

// RecordTask 为异步任务的真实终态写审计（无 HTTP 上下文：由后台 worker/对账器调用）。
func (s *Service) RecordTask(userID *int64, module, action, targetType, targetID, result, detail string) {
	e := store.AuditLog{
		Ts:         time.Now().Unix(),
		Module:     module,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Result:     result,
		Detail:     detail,
	}
	if userID != nil {
		e.UserID = userID
		if u, err := s.store.UserByID(*userID); err == nil {
			e.Username = u.Username
		}
	}
	if err := s.store.InsertAuditLog(&e); err != nil {
		log.Printf("审计记录写入失败（%s/%s）: %v", e.Module, e.Action, err)
	}
}

// Query 分页查询。
func (s *Service) Query(f store.AuditFilter) ([]store.AuditLog, int64, error) {
	return s.store.QueryAuditLogs(f)
}

// Login 记录登录事件（成功/失败均记录）。
func (s *Service) Login(e auth.LoginEvent) {
	result := ResultSuccess
	if !e.Success {
		result = ResultFailure
	}
	entry := &store.AuditLog{
		Ts:       time.Now().Unix(),
		Username: e.Username,
		IP:       e.IP,
		Module:   "auth",
		Action:   "login",
		Result:   result,
		Detail:   e.Detail,
	}
	if err := s.store.InsertAuditLog(entry); err != nil {
		log.Printf("审计记录写入失败（auth/login）: %v", err)
	}
}

// RetentionCleanup 按保留天数清理，返回删除条数。
func (s *Service) RetentionCleanup(days int) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	cut := time.Now().AddDate(0, 0, -days).Unix()
	return s.store.DeleteAuditLogsBefore(cut)
}

// RequestIDMiddleware 为每个请求注入请求 ID（优先沿用入站头）。
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := strings.TrimSpace(r.Header.Get("X-Request-Id"))
		if rid == "" {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			rid = hex.EncodeToString(b)
		}
		ctx := context.WithValue(r.Context(), requestIDKey, rid)
		w.Header().Set("X-Request-Id", rid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFrom 读取上下文请求 ID。
func RequestIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

var sensitiveKeys = []string{"password", "passwd", "secret", "token", "register_token"}

// DetailJSON 将参数序列化并脱敏敏感键（key 含 password/secret/token）。
func DetailJSON(m map[string]any) string {
	cp := make(map[string]any, len(m))
	for k, v := range m {
		lk := strings.ToLower(k)
		hit := false
		for _, sk := range sensitiveKeys {
			if strings.Contains(lk, sk) {
				hit = true
				break
			}
		}
		if hit {
			cp[k] = "***"
		} else {
			cp[k] = v
		}
	}
	b, err := json.Marshal(cp)
	if err != nil {
		return ""
	}
	return string(b)
}
