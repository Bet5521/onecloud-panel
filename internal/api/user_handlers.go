package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/auth"
	"onecloud-panel/internal/notify"
	"onecloud-panel/internal/store"
)

type userDTO struct {
	ID              int64    `json:"id"`
	Username        string   `json:"username"`
	RealName        string   `json:"real_name"`
	Phone           string   `json:"phone"`
	NotifyMethod    string   `json:"notify_method"`
	NotifyEmail     string   `json:"notify_email"`
	NotifySMSPhone  string   `json:"notify_sms_phone"`
	NotifyChannelID int64    `json:"notify_channel_id"` // 0 = 未指定
	NotifyTarget    string   `json:"notify_target"`     // 每用户接收标识(UUID/Key/手机号)
	NotifyEvents    []string `json:"notify_events"`     // 订阅的事件编码列表
	RoleID          int64    `json:"role_id"`
	RoleCode        string   `json:"role_code"`
	RoleName        string   `json:"role_name"`
	Status          string   `json:"status"`
	CreatedAt       int64    `json:"created_at"`
	SuperCode       string   `json:"super_code,omitempty"` // 仅创建/重置响应返回一次明文
}

func (a *API) toUserDTO(u store.User, roles []store.Role) userDTO {
	d := userDTO{ID: u.ID, Username: u.Username, RealName: u.RealName, Phone: u.Phone,
		NotifyMethod: u.NotifyMethod, NotifyEmail: u.NotifyEmail, NotifySMSPhone: u.NotifySMSPhone,
		NotifyTarget: derefStr(u.NotifyTarget),
		RoleID:       u.RoleID, Status: u.Status, CreatedAt: u.CreatedAt}
	if u.NotifyChannelID != nil {
		d.NotifyChannelID = *u.NotifyChannelID
	}
	if evs, err := a.store.ListUserEvents(u.ID); err == nil && len(evs) > 0 {
		d.NotifyEvents = evs
	} else {
		d.NotifyEvents = []string{}
	}
	for _, r := range roles {
		if r.ID == u.RoleID {
			d.RoleCode = r.Code
			d.RoleName = r.Name
			break
		}
	}
	return d
}

// validNotifyMethods 用户级通知方式枚举（email/sms 已并入通道体系，仅保留 log/channel）。
var validNotifyMethods = map[string]bool{"log": true, "channel": true, "email": true, "sms": true}

// methodFromChannel 由通道选择推导通知方式：选中通道为 channel，否则仅面板日志。
func methodFromChannel(channelID int64) string {
	if channelID > 0 {
		return "channel"
	}
	return "log"
}

// validateNotify 校验用户通知方式：method 仅 log/channel/email/sms；
// email/sms 走平台(SMTP/短信网关)，不依赖用户绑定的通知通道、不要求接收标识；
// channel 必须为已启用通道，且若通道需要每用户接收标识则 target 必填。
func (a *API) validateNotify(method string, channelID int64, target *string) error {
	if !validNotifyMethods[method] {
		return errors.New("通知方式仅支持 log/channel/email/sms")
	}
	if method == "log" || method == "email" || method == "sms" {
		return nil
	}
	if channelID <= 0 {
		return errors.New("必须选择通知通道：请指定 notify_channel_id")
	}
	ch, err := a.store.NotificationChannelByID(channelID)
	if err != nil {
		return errors.New("通知通道不存在")
	}
	if !ch.Enabled {
		return errors.New("通知通道未启用")
	}
	if meta := notify.TargetFor(ch.Type); meta.Needed && strings.TrimSpace(derefStr(target)) == "" {
		return errors.New("该通道需要填写接收标识：" + meta.Label)
	}
	return nil
}

// channelPtr 把 0 归一化为 nil（未指定）。
func channelPtr(id int64) *int64 {
	if id <= 0 {
		return nil
	}
	return &id
}

// derefStr 安全解引用 *string；nil 返回空串（用于可为 NULL 的 notify_target 等列）。
func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

type notifyChannelOption struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	NeedsTarget  bool   `json:"needs_target"`  // 是否需要每用户接收标识
	TargetLabel  string `json:"target_label"`  // 接收标识表单标签
	TargetHint   string `json:"target_hint"`   // 接收标识提示
	TargetSecret bool   `json:"target_secret"` // 是否敏感(密码框)
}

// GET /api/auth/notify-channels — 本人可选的通知通道（启用中，不含任何密钥）。
// 无需 settings:read，普通用户也能维护自己的通知方式。
func (a *API) listMyNotifyChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := a.store.EnabledNotificationChannels()
	if err != nil {
		writeError(w, 500, "通知通道查询失败")
		return
	}
	out := make([]notifyChannelOption, 0, len(channels))
	for _, c := range channels {
		// 配置未完成的通道不出现在用户可选列表（满足「未配置的不可选」）
		if _, berr := notify.Build(c.Type, parseConfig(c.ConfigJSON)); berr != nil {
			continue
		}
		meta := notify.TargetFor(c.Type)
		out = append(out, notifyChannelOption{
			ID:           c.ID,
			Name:         c.Name,
			Type:         c.Type,
			NeedsTarget:  meta.Needed,
			TargetLabel:  meta.Label,
			TargetHint:   meta.Hint,
			TargetSecret: meta.Secret,
		})
	}
	writeJSON(w, map[string]any{"items": out})
}

// GET /api/users
func (a *API) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.store.ListUsers()
	if err != nil {
		writeError(w, 500, "用户查询失败")
		return
	}
	roles, _ := a.store.ListRoles()
	out := make([]userDTO, 0, len(users))
	for _, u := range users {
		out = append(out, a.toUserDTO(u, roles))
	}
	writeJSON(w, map[string]any{"items": out, "total": len(out)})
}

// newSuperCode 为用户生成新的超级验证码并落库哈希，返回明文。
func (a *API) newSuperCode(userID int64) (string, error) {
	code, err := auth.GenerateSuperCode()
	if err != nil {
		return "", err
	}
	if err := a.store.UpdateUserSuperCode(userID, auth.HashSuperCode(code)); err != nil {
		return "", err
	}
	return code, nil
}

type createUserReq struct {
	Username        string   `json:"username"`
	Password        string   `json:"password"`
	RoleID          int64    `json:"role_id"`
	RealName        string   `json:"real_name"`
	Phone           string   `json:"phone"`
	NotifyMethod    string   `json:"notify_method"`
	NotifyEmail     string   `json:"notify_email"`
	NotifySMSPhone  string   `json:"notify_sms_phone"`
	NotifyChannelID int64    `json:"notify_channel_id"`
	NotifyTarget    *string  `json:"notify_target"`
	NotifyEvents    []string `json:"notify_events"`
}

// POST /api/users
func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	var req createUserReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.RealName = strings.TrimSpace(req.RealName)
	req.Phone = strings.TrimSpace(req.Phone)
	req.NotifyEmail = strings.TrimSpace(req.NotifyEmail)
	req.NotifySMSPhone = strings.TrimSpace(req.NotifySMSPhone)
	if req.NotifyTarget != nil {
		s := strings.TrimSpace(*req.NotifyTarget)
		req.NotifyTarget = &s
	}
	if len(req.Username) < 3 || len(req.Username) > 32 {
		writeError(w, 400, "用户名长度需为 3-32")
		return
	}
	if err := auth.ValidateUsername(req.Username); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if len(req.Password) < 8 || len(req.Password) > 128 {
		writeError(w, 400, "密码长度需为 8-128")
		return
	}
	if len(req.RealName) > 32 {
		writeError(w, 400, "姓名长度需不超过 32")
		return
	}
	if len(req.Phone) > 24 {
		writeError(w, 400, "手机号长度需不超过 24")
		return
	}
	if req.NotifyMethod == "" {
		req.NotifyMethod = methodFromChannel(req.NotifyChannelID)
	} else if !validNotifyMethods[req.NotifyMethod] {
		writeError(w, 400, "通知方式仅支持 log/channel/email/sms")
		return
	}
	if err := a.validateNotify(req.NotifyMethod, req.NotifyChannelID, req.NotifyTarget); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	notifyEvents, err := normalizeEvents(req.NotifyEvents)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if _, err := a.store.RoleByID(req.RoleID); err != nil {
		writeError(w, 400, "角色不存在")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, 500, "密码处理失败")
		return
	}
	u, err := a.store.CreateUserWithProfile(req.Username, hash, req.RoleID, req.RealName, req.Phone)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeError(w, 409, "用户名已存在")
			return
		}
		writeError(w, 500, "创建用户失败")
		return
	}
	if err := a.store.UpdateUserNotify(u.ID, req.NotifyMethod, req.NotifyEmail,
		req.NotifySMSPhone, channelPtr(req.NotifyChannelID), req.NotifyTarget); err != nil {
		writeError(w, 500, "通知方式保存失败")
		return
	}
	if req.NotifyEvents != nil {
		if err := a.store.SetUserEvents(u.ID, notifyEvents); err != nil {
			writeError(w, 500, "事件订阅保存失败")
			return
		}
	}
	u, err = a.store.UserByID(u.ID)
	if err != nil {
		writeError(w, 500, "创建用户失败")
		return
	}
	// 注册即生成超级验证码，明文仅本次响应返回，提示用户妥善保存
	code, err := a.newSuperCode(u.ID)
	if err != nil {
		writeError(w, 500, "超级验证码生成失败")
		return
	}
	a.audit.Record(r, "user", "create", "user", strconv.FormatInt(u.ID, 10),
		audit.ResultSuccess, audit.DetailJSON(map[string]any{
			"username": u.Username, "role_id": u.RoleID,
		}))
	roles, _ := a.store.ListRoles()
	dto := a.toUserDTO(*u, roles)
	dto.SuperCode = code
	writeJSON(w, dto)
}

type updateUserReq struct {
	RoleID          *int64   `json:"role_id"`
	Status          *string  `json:"status"`
	RealName        *string  `json:"real_name"`
	Phone           *string  `json:"phone"`
	NotifyMethod    *string  `json:"notify_method"`
	NotifyEmail     *string  `json:"notify_email"`
	NotifySMSPhone  *string  `json:"notify_sms_phone"`
	NotifyChannelID *int64   `json:"notify_channel_id"` // 0 = 清除
	NotifyTarget    *string  `json:"notify_target"`
	NotifyEvents    []string `json:"notify_events"`
}

// PUT /api/users/{id}
func (a *API) updateUser(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, 400, "ID 非法")
		return
	}
	target, err := a.store.UserByID(id)
	if err != nil {
		writeError(w, 404, "用户不存在")
		return
	}
	var req updateUserReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	selfID := int64(0)
	if ident := auth.FromContext(r.Context()); ident != nil && ident.User != nil {
		selfID = ident.User.ID
	}

	if req.Status != nil {
		s := *req.Status
		if s != "active" && s != "disabled" {
			writeError(w, 400, "状态仅支持 active/disabled")
			return
		}
		if id == selfID && s == "disabled" {
			writeError(w, 400, "不能禁用当前登录的自己")
			return
		}
	}
	if req.RoleID != nil {
		if _, err := a.store.RoleByID(*req.RoleID); err != nil {
			writeError(w, 400, "角色不存在")
			return
		}
	}
	// 事件订阅先行校验（在应用任何变更前），避免部分生效
	var notifyEvents []string
	if req.NotifyEvents != nil {
		evs, nerr := normalizeEvents(req.NotifyEvents)
		if nerr != nil {
			writeError(w, 400, nerr.Error())
			return
		}
		notifyEvents = evs
	}
	realName, phone := "", ""
	hasProfile := false
	if req.RealName != nil || req.Phone != nil {
		hasProfile = true
		if req.RealName != nil {
			realName = strings.TrimSpace(*req.RealName)
		}
		if req.Phone != nil {
			phone = strings.TrimSpace(*req.Phone)
		}
		if len(realName) > 32 {
			writeError(w, 400, "姓名长度需不超过 32")
			return
		}
		if len(phone) > 24 {
			writeError(w, 400, "手机号长度需不超过 24")
			return
		}
	}

	// 若目标当前是 active 管理员，降级（禁用/改非管理员角色）后仍需 1 个其他管理员。
	if isActiveAdmin(a.store, target) {
		n, err := a.store.CountActiveAdmins(id)
		if err != nil {
			writeError(w, 500, "管理员校验失败")
			return
		}
		// 目标若改回管理员+active 则仍占名额；检查请求是否为降级变更
		downgrade := req.Status != nil && *req.Status == "disabled"
		if !downgrade && req.RoleID != nil {
			if rl, e := a.store.RoleByID(*req.RoleID); e == nil && rl.Code != "admin" {
				downgrade = true
			}
		}
		if downgrade && n < 1 {
			writeError(w, 400, "必须保留至少一个启用的管理员")
			return
		}
	}

	if req.Status != nil {
		if err := a.store.UpdateUserStatus(id, *req.Status); err != nil {
			writeError(w, 500, "状态更新失败")
			return
		}
	}
	if req.RoleID != nil {
		if err := a.store.UpdateUserRole(id, *req.RoleID); err != nil {
			writeError(w, 500, "角色更新失败")
			return
		}
	}
	if hasProfile {
		if err := a.store.UpdateUserProfile(id, realName, phone); err != nil {
			writeError(w, 500, "资料更新失败")
			return
		}
	}
	if req.NotifyMethod != nil || req.NotifyEmail != nil ||
		req.NotifySMSPhone != nil || req.NotifyChannelID != nil || req.NotifyTarget != nil {
		method, email, smsPhone := target.NotifyMethod, target.NotifyEmail, target.NotifySMSPhone
		chID := int64(0)
		if target.NotifyChannelID != nil {
			chID = *target.NotifyChannelID
		}
		tgt := target.NotifyTarget
		if req.NotifyEmail != nil {
			email = strings.TrimSpace(*req.NotifyEmail)
		}
		if req.NotifySMSPhone != nil {
			smsPhone = strings.TrimSpace(*req.NotifySMSPhone)
		}
		if req.NotifyChannelID != nil {
			chID = *req.NotifyChannelID
		}
		if req.NotifyTarget != nil {
			s := strings.TrimSpace(*req.NotifyTarget)
			tgt = &s
		}
		if req.NotifyMethod != nil && *req.NotifyMethod != "" {
			method = *req.NotifyMethod
		} else {
			method = methodFromChannel(chID)
		}
		if err := a.validateNotify(method, chID, tgt); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		if err := a.store.UpdateUserNotify(id, method, email, smsPhone, channelPtr(chID), tgt); err != nil {
			writeError(w, 500, "通知方式更新失败")
			return
		}
	}
	if req.NotifyEvents != nil {
		if err := a.store.SetUserEvents(id, notifyEvents); err != nil {
			writeError(w, 500, "事件订阅保存失败")
			return
		}
	}
	a.audit.Record(r, "user", "update", "user", strconv.FormatInt(id, 10),
		audit.ResultSuccess, audit.DetailJSON(map[string]any{
			"role_id": req.RoleID, "status": req.Status,
			"real_name": req.RealName, "phone": req.Phone,
			"notify_method": req.NotifyMethod,
		}))

	u, err := a.store.UserByID(id)
	if err != nil || u == nil {
		writeError(w, 500, "用户读取失败")
		return
	}
	roles, _ := a.store.ListRoles()
	writeJSON(w, a.toUserDTO(*u, roles))
}

type passwordReq struct {
	Password string `json:"password"`
}

// POST /api/users/{id}/password — 管理员重置他人密码。
func (a *API) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, 400, "ID 非法")
		return
	}
	if _, err := a.store.UserByID(id); err != nil {
		writeError(w, 404, "用户不存在")
		return
	}
	var req passwordReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	if len(req.Password) < 8 || len(req.Password) > 128 {
		writeError(w, 400, "密码长度需为 8-128")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, 500, "密码处理失败")
		return
	}
	if err := a.store.UpdateUserPassword(id, hash); err != nil {
		writeError(w, 500, "密码更新失败")
		return
	}
	// 管理员重置密码后轮换超级验证码，明文仅本次响应返回
	code, err := a.newSuperCode(id)
	if err != nil {
		writeError(w, 500, "超级验证码生成失败")
		return
	}
	// 密码变更后注销该用户全部会话，强制重新登录
	if err := a.store.DeleteUserSessions(id); err != nil {
		writeError(w, 500, "会话清理失败")
		return
	}
	a.audit.Record(r, "user", "reset_password", "user",
		strconv.FormatInt(id, 10), audit.ResultSuccess, "")
	writeJSON(w, map[string]string{"status": "ok", "super_code": code})
}

// POST /api/users/{id}/super-code — 管理员重新生成超级验证码。
func (a *API) regenerateSuperCode(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, 400, "ID 非法")
		return
	}
	if _, err := a.store.UserByID(id); err != nil {
		writeError(w, 404, "用户不存在")
		return
	}
	code, err := a.newSuperCode(id)
	if err != nil {
		writeError(w, 500, "超级验证码生成失败")
		return
	}
	a.audit.Record(r, "user", "rotate_super_code", "user",
		strconv.FormatInt(id, 10), audit.ResultSuccess, "")
	writeJSON(w, map[string]string{"status": "ok", "super_code": code})
}

type myProfileReq struct {
	RealName        string   `json:"real_name"`
	Phone           string   `json:"phone"`
	NotifyMethod    *string  `json:"notify_method"`
	NotifyEmail     *string  `json:"notify_email"`
	NotifySMSPhone  *string  `json:"notify_sms_phone"`
	NotifyChannelID *int64   `json:"notify_channel_id"` // 0 = 清除
	NotifyTarget    *string  `json:"notify_target"`
	NotifyEvents    []string `json:"notify_events"`
}

// PUT /api/auth/profile — 本人更新姓名、手机号与通知方式。
func (a *API) updateMyProfile(w http.ResponseWriter, r *http.Request) {
	ident := auth.FromContext(r.Context())
	if ident == nil || ident.User == nil {
		writeError(w, 401, "未登录")
		return
	}
	var req myProfileReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	realName := strings.TrimSpace(req.RealName)
	phone := strings.TrimSpace(req.Phone)
	if len(realName) > 32 {
		writeError(w, 400, "姓名长度需不超过 32")
		return
	}
	if len(phone) > 24 {
		writeError(w, 400, "手机号长度需不超过 24")
		return
	}
	if err := a.store.UpdateUserProfile(ident.User.ID, realName, phone); err != nil {
		writeError(w, 500, "资料更新失败")
		return
	}
	notifyChanged := req.NotifyMethod != nil || req.NotifyEmail != nil ||
		req.NotifySMSPhone != nil || req.NotifyChannelID != nil || req.NotifyTarget != nil
	if notifyChanged {
		cur, err := a.store.UserByID(ident.User.ID)
		if err != nil {
			writeError(w, 500, "资料读取失败")
			return
		}
		method, email, smsPhone := cur.NotifyMethod, cur.NotifyEmail, cur.NotifySMSPhone
		chID := int64(0)
		if cur.NotifyChannelID != nil {
			chID = *cur.NotifyChannelID
		}
		tgt := cur.NotifyTarget
		if req.NotifyEmail != nil {
			email = strings.TrimSpace(*req.NotifyEmail)
		}
		if req.NotifySMSPhone != nil {
			smsPhone = strings.TrimSpace(*req.NotifySMSPhone)
		}
		if req.NotifyChannelID != nil {
			chID = *req.NotifyChannelID
		}
		if req.NotifyTarget != nil {
			s := strings.TrimSpace(*req.NotifyTarget)
			tgt = &s
		}
		if req.NotifyMethod != nil && *req.NotifyMethod != "" {
			method = *req.NotifyMethod
		} else {
			method = methodFromChannel(chID)
		}
		if err := a.validateNotify(method, chID, tgt); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		if err := a.store.UpdateUserNotify(ident.User.ID, method, email, smsPhone, channelPtr(chID), tgt); err != nil {
			writeError(w, 500, "通知方式更新失败")
			return
		}
	}
	if req.NotifyEvents != nil {
		evs, nerr := normalizeEvents(req.NotifyEvents)
		if nerr != nil {
			writeError(w, 400, nerr.Error())
			return
		}
		if err := a.store.SetUserEvents(ident.User.ID, evs); err != nil {
			writeError(w, 500, "事件订阅保存失败")
			return
		}
	}
	a.audit.Record(r, "user", "update_profile", "user",
		strconv.FormatInt(ident.User.ID, 10), audit.ResultSuccess, "")
	writeJSON(w, map[string]string{"status": "ok"})
}

// DELETE /api/users/{id}
func (a *API) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		writeError(w, 400, "ID 非法")
		return
	}
	if _, err := a.store.UserByID(id); err != nil {
		writeError(w, 404, "用户不存在")
		return
	}
	if ident := auth.FromContext(r.Context()); ident != nil && ident.User != nil && ident.User.ID == id {
		writeError(w, 400, "不能删除当前登录的自己")
		return
	}
	if n, err := a.store.CountActiveAdmins(id); err != nil {
		writeError(w, 500, "管理员校验失败")
		return
	} else {
		target, _ := a.store.UserByID(id)
		if isActiveAdmin(a.store, target) && n < 1 {
			writeError(w, 400, "必须保留至少一个启用的管理员")
			return
		}
	}
	if err := a.store.DeleteUser(id); err != nil {
		writeError(w, 500, "删除失败")
		return
	}
	a.audit.Record(r, "user", "delete", "user",
		strconv.FormatInt(id, 10), audit.ResultSuccess, "")
	writeJSON(w, map[string]string{"status": "ok"})
}

type changePasswordReq struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// POST /api/auth/change-password — 本人修改密码（任何登录用户）。
func (a *API) changePassword(w http.ResponseWriter, r *http.Request) {
	ident := auth.FromContext(r.Context())
	if ident == nil || ident.User == nil {
		writeError(w, 401, "未登录")
		return
	}
	var req changePasswordReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	ok, err := auth.VerifyPassword(req.OldPassword, ident.User.PasswordHash)
	if err != nil {
		writeError(w, 500, "密码校验失败")
		return
	}
	if !ok {
		writeError(w, 400, "原密码不正确")
		return
	}
	if len(req.NewPassword) < 8 || len(req.NewPassword) > 128 {
		writeError(w, 400, "新密码长度需为 8-128")
		return
	}
	if req.NewPassword == req.OldPassword {
		writeError(w, 400, "新密码不能与原密码相同")
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, 500, "密码处理失败")
		return
	}
	if err := a.store.UpdateUserPassword(ident.User.ID, hash); err != nil {
		writeError(w, 500, "密码更新失败")
		return
	}
	// 改密后注销其他设备上的会话（保留当前登录），防止凭据泄露后被继续使用
	revoked := 0
	if a.sessions != nil {
		if n, serr := a.sessions.RevokeOtherSessions(ident.User.ID, a.sessions.CurrentSID(r)); serr == nil {
			revoked = n
		}
	}
	a.audit.Record(r, "user", "change_password", "user",
		strconv.FormatInt(ident.User.ID, 10), audit.ResultSuccess, "")
	writeJSON(w, map[string]any{"status": "ok", "revoked_sessions": revoked})
}

// GET /api/auth/my-notify-events — 本人订阅的通知事件。
func (a *API) getMyNotifyEvents(w http.ResponseWriter, r *http.Request) {
	ident := auth.FromContext(r.Context())
	if ident == nil || ident.User == nil {
		writeError(w, 401, "未登录")
		return
	}
	evs := []string{}
	if list, err := a.store.ListUserEvents(ident.User.ID); err == nil && len(list) > 0 {
		evs = list
	}
	writeJSON(w, map[string]any{"events": evs})
}

type myNotifyEventsReq struct {
	Events []string `json:"events"`
}

// PUT /api/auth/notify-events — 本人更新事件订阅。
func (a *API) updateMyNotifyEvents(w http.ResponseWriter, r *http.Request) {
	ident := auth.FromContext(r.Context())
	if ident == nil || ident.User == nil {
		writeError(w, 401, "未登录")
		return
	}
	var req myNotifyEventsReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	evs, err := normalizeEvents(req.Events)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := a.store.SetUserEvents(ident.User.ID, evs); err != nil {
		writeError(w, 500, "保存失败")
		return
	}
	a.audit.Record(r, "user", "update_notify_events", "user",
		strconv.FormatInt(ident.User.ID, 10), audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"events": evs}))
	if evs == nil {
		evs = []string{}
	}
	writeJSON(w, map[string]any{"events": evs})
}

func isActiveAdmin(s *store.Store, u *store.User) bool {
	if u == nil || u.Status != "active" {
		return false
	}
	role, err := s.RoleByID(u.RoleID)
	if err != nil {
		return false
	}
	return role.Code == "admin"
}
