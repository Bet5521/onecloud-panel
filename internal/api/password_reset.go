package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/auth"
	"onecloud-panel/internal/store"
)

const (
	resetCodeLen  = 8
	resetTTL      = 15 * time.Minute
	resetAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789" // 去除易混字符 I/L/O/0/1
)

// 重置码确认接口的防爆破限流：15 分钟内累计 10 次失败即拒绝。
const (
	resetConfirmMaxFails = 10
	resetConfirmWindow   = 15 * time.Minute
)

// SetResetLimiter 注入重置接口限流器。
func (a *API) SetResetLimiter(l *auth.LoginLimiter) {
	a.resetLimit = l
}

type resetRequestReq struct {
	Username string `json:"username"`
	Method   string `json:"method"` // email（默认）/ notification / super_code
}

// resetMethod 归一化重置方式。
func resetMethod(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "notification":
		return "notification"
	case "super_code":
		return "super_code"
	default:
		return "email"
	}
}

// POST /api/auth/password-reset/request
// 出于用户枚举防护，无论用户是否存在均返回相同成功响应。
func (a *API) resetRequest(w http.ResponseWriter, r *http.Request) {
	var req resetRequestReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	username := strings.TrimSpace(req.Username)
	method := resetMethod(req.Method)
	ip := auth.ClientIP(r)

	// 限流：每 IP 10 分钟最多 5 次
	if a.resetLimit == nil {
		a.resetLimit = auth.NewLoginLimiter(5, 10*time.Minute)
	}
	if !a.resetLimit.Allow(ip) {
		writeError(w, http.StatusTooManyRequests, "重置请求过于频繁，请 10 分钟后再试")
		return
	}
	a.resetLimit.Fail(ip)

	// 超级验证码方式：无需下发短期码，前端直接进入确认步骤
	if method == "super_code" {
		writeJSON(w, map[string]any{
			"status": "ok",
			"method": method,
			"hint":   "请输入超级验证码并设置新密码",
		})
		return
	}

	u, err := a.store.UserByUsername(username)
	if err == nil && u.Status == "active" {
		code, err := genResetCode()
		if err == nil {
			now := time.Now()
			_ = a.store.CreatePasswordReset(&store.PasswordReset{
				UserID:    u.ID,
				CodeHash:  hashResetCode(code),
				ExpiresAt: now.Add(resetTTL).Unix(),
				CreatedAt: now.Unix(),
			})

			// 按用户绑定的通知方式（或邮箱方式）定向下发
			delivery, derr := a.deliverResetCodeByMethod(u, method, code)
			if derr != nil {
				log.Printf("[密码重置] %s 方式下发失败: %v", method, derr)
			}
			if delivery == "" {
				// 兜底：仅当无可用下发渠道（含用户选择日志方式）时才把验证码写入服务日志，
				// 由具备服务器访问权限的管理员取用；面板内 journal 视图会过滤此类行
				log.Printf("===== [密码重置] 用户 %s 的重置码：%s （15 分钟内有效） =====",
					username, code)
				delivery = "无可用下发渠道，已输出到服务日志"
			} else {
				// 下发成功：日志只记录渠道，不落验证码
				log.Printf("[密码重置] 用户 %s 的重置码已通过 %s 下发", username, delivery)
			}

			if a.codeSink != nil {
				a.codeSink(username, code)
			}
		}
	}

	hint := resetDeliveryHint()
	if method == "notification" {
		hint = "若用户名有效，重置码已按该用户绑定的通知方式定向下发（邮箱/短信/通知通道）；" +
			"仅当无可用渠道时才输出到服务日志（journalctl -u onecloud-panel）"
	}
	writeJSON(w, map[string]any{
		"status": "ok",
		"method": method,
		"hint":   hint,
	})
}

type resetConfirmReq struct {
	Username    string `json:"username"`
	Code        string `json:"code"`
	NewPassword string `json:"new_password"`
	Method      string `json:"method"` // email/notification 走重置码；super_code 走超级验证码
}

// POST /api/auth/password-reset/confirm
func (a *API) resetConfirm(w http.ResponseWriter, r *http.Request) {
	var req resetConfirmReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	username := strings.TrimSpace(req.Username)
	code := strings.ToUpper(strings.TrimSpace(req.Code))
	method := resetMethod(req.Method)
	ip := auth.ClientIP(r)

	u, err := a.store.UserByUsername(username)
	if err != nil || u.Status != "active" {
		writeError(w, http.StatusBadRequest, "验证码无效或已过期")
		return
	}
	if len(req.NewPassword) < 8 || len(req.NewPassword) > 128 {
		writeError(w, http.StatusBadRequest, "新密码长度需为 8-128 位")
		return
	}

	if method == "super_code" {
		a.confirmBySuperCode(w, r, ip, u, code, req.NewPassword)
		return
	}
	a.confirmByResetCode(w, r, u, code, req.NewPassword)
}

// confirmBySuperCode 超级验证码验证：直接设新密码，成功后轮换超级验证码。
func (a *API) confirmBySuperCode(w http.ResponseWriter, r *http.Request,
	ip string, u *store.User, code, newPassword string) {
	// 防爆破：与重置请求共用每 IP 限流器
	if a.resetLimit == nil {
		a.resetLimit = auth.NewLoginLimiter(5, 10*time.Minute)
	}
	if !a.resetLimit.Allow(ip) {
		writeError(w, http.StatusTooManyRequests, "尝试过于频繁，请 10 分钟后再试")
		return
	}
	if !auth.VerifySuperCode(code, u.SuperCode) {
		a.resetLimit.Fail(ip)
		a.audit.RecordTask(nil, "auth", "password-reset",
			"user", u.Username, audit.ResultFailure, "超级验证码错误")
		writeError(w, http.StatusBadRequest, "超级验证码不正确")
		return
	}
	a.resetLimit.Reset(ip)

	newCode, err := a.finishPasswordReset(u, newPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.audit.RecordTask(nil, "auth", "password-reset",
		"user", u.Username, audit.ResultSuccess, "超级验证码自助重置成功")
	writeJSON(w, map[string]string{
		"status":     "ok",
		"super_code": newCode,
		"hint":       "密码已重置，请使用新密码登录；超级验证码已刷新，请妥善保存新码",
	})
}

// confirmByResetCode 短期重置码验证（邮箱/通知渠道下发）。
func (a *API) confirmByResetCode(w http.ResponseWriter, r *http.Request,
	u *store.User, code, newPassword string) {
	// 防爆破：重置码 8 位、15 分钟有效，不限流则可暴力枚举。
	// 与超级验证码路径一样按 IP + 用户名双键限流，超限直接拒绝。
	ip := auth.ClientIP(r)
	if a.confirmLimit == nil {
		a.confirmLimit = auth.NewLoginLimiter(resetConfirmMaxFails, resetConfirmWindow)
	}
	if !a.confirmLimit.Allow(ip) || !a.confirmLimit.Allow(u.Username) {
		writeError(w, http.StatusTooManyRequests, "尝试过于频繁，请稍后再试")
		return
	}
	rec, err := a.store.GetActivePasswordReset(u.ID, time.Now().Unix())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "重置记录查询失败")
		return
	}
	if rec == nil {
		writeError(w, http.StatusBadRequest, "重置码无效或已过期，请重新申请")
		return
	}

	want, _ := hex.DecodeString(rec.CodeHash)
	got := sha256.Sum256([]byte(code))
	if subtle.ConstantTimeCompare(want, got[:]) != 1 {
		a.confirmLimit.Fail(ip)
		a.confirmLimit.Fail(u.Username)
		writeError(w, http.StatusBadRequest, "重置码无效或已过期")
		return
	}
	a.confirmLimit.Reset(ip)
	a.confirmLimit.Reset(u.Username)

	newCode, err := a.finishPasswordReset(u, newPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := a.store.MarkPasswordResetUsed(rec.ID); err != nil {
		log.Printf("[密码重置] 标记已使用失败: %v", err)
	}
	if err := a.store.DeleteUserPasswordResets(u.ID); err != nil {
		log.Printf("[密码重置] 清理旧记录失败: %v", err)
	}

	a.audit.RecordTask(nil, "auth", "password-reset",
		"user", u.Username, audit.ResultSuccess, "重置码自助重置成功")

	writeJSON(w, map[string]string{
		"status":     "ok",
		"super_code": newCode,
		"hint":       "密码已重置，请使用新密码登录；超级验证码已刷新，请妥善保存新码",
	})
}

// finishPasswordReset 落库新密码、轮换超级验证码并注销全部会话；返回新验证码明文。
func (a *API) finishPasswordReset(u *store.User, newPassword string) (string, error) {
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return "", errors.New("密码处理失败")
	}
	if err := a.store.UpdateUserPassword(u.ID, hash); err != nil {
		return "", errors.New("密码更新失败")
	}
	newCode, err := a.newSuperCode(u.ID)
	if err != nil {
		return "", errors.New("超级验证码生成失败")
	}
	// 注销该用户全部会话，强制重新登录
	if err := a.store.DeleteUserSessions(u.ID); err != nil {
		log.Printf("[密码重置] 会话清理失败: %v", err)
	}
	return newCode, nil
}

func genResetCode() (string, error) {
	return auth.RandomCode(resetAlphabet, resetCodeLen)
}

func hashResetCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// resetDeliveryHint 默认（邮箱方式）提示文案。
func resetDeliveryHint() string {
	return "若用户名有效，重置码已发送至该用户绑定的邮箱（未绑定时发往 SMTP 收件人设置）；" +
		"仅当无可用渠道时才输出到服务日志（journalctl -u onecloud-panel）"
}
