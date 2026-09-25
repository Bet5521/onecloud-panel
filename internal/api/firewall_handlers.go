package api

import (
	"net/http"
	"strconv"

	"onecloud-panel/internal/audit"
	"onecloud-panel/internal/firewall"
)

// GET /api/nodes/{id}/firewall — 检测防火墙后端、启停状态与当前规则。
func (a *API) nodeFirewallStatus(w http.ResponseWriter, r *http.Request) {
	n, ok := a.scopedNode(w, r)
	if !ok {
		return
	}
	st, err := a.firewall.Detect(r.Context(), n)
	if err != nil {
		writeError(w, http.StatusBadGateway, "防火墙检测失败: "+err.Error())
		return
	}
	writeJSON(w, st)
}

type firewallRuleReq struct {
	Port   string `json:"port"`
	Proto  string `json:"proto"`
	Action string `json:"action"`
	Source string `json:"source"`
}

// POST /api/nodes/{id}/firewall/rules — 新增端口规则（both 协议自动拆 tcp+udp）。
func (a *API) nodeFirewallAddRule(w http.ResponseWriter, r *http.Request) {
	a.mutateFirewallRule(w, r, false)
}

// POST /api/nodes/{id}/firewall/rules/remove — 删除端口规则。
func (a *API) nodeFirewallRemoveRule(w http.ResponseWriter, r *http.Request) {
	a.mutateFirewallRule(w, r, true)
}

func (a *API) mutateFirewallRule(w http.ResponseWriter, r *http.Request, remove bool) {
	n, ok := a.scopedNode(w, r)
	if !ok {
		return
	}
	var req firewallRuleReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	rule := firewall.Rule{Port: req.Port, Proto: req.Proto, Action: req.Action, Source: req.Source}
	if err := firewall.ValidateRule(rule); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	action, op := "firewall_rule_add", "新增"
	if remove {
		action, op = "firewall_rule_remove", "删除"
	}
	detail := map[string]any{"port": req.Port, "proto": req.Proto, "action": req.Action, "source": req.Source}
	var err error
	if remove {
		err = a.firewall.RemoveRule(r.Context(), n, rule)
	} else {
		err = a.firewall.AddRule(r.Context(), n, rule)
	}
	if err != nil {
		detail["error"] = err.Error()
		a.audit.Record(r, "node", action, "node", strconv.FormatInt(n.ID, 10), audit.ResultFailure,
			audit.DetailJSON(detail))
		writeError(w, http.StatusBadGateway, op+"规则失败: "+err.Error())
		return
	}
	a.audit.Record(r, "node", action, "node", strconv.FormatInt(n.ID, 10), audit.ResultSuccess,
		audit.DetailJSON(detail))
	writeJSON(w, map[string]string{"status": "ok"})
}

type firewallToggleReq struct {
	Enabled bool `json:"enabled"`
}

// POST /api/nodes/{id}/firewall/toggle — 开启/关闭防火墙（仅 ufw / firewalld 支持）。
func (a *API) nodeFirewallToggle(w http.ResponseWriter, r *http.Request) {
	n, ok := a.scopedNode(w, r)
	if !ok {
		return
	}
	var req firewallToggleReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := a.firewall.Toggle(r.Context(), n, req.Enabled); err != nil {
		a.audit.Record(r, "node", "firewall_toggle", "node", strconv.FormatInt(n.ID, 10), audit.ResultFailure,
			audit.DetailJSON(map[string]any{"enabled": req.Enabled, "error": err.Error()}))
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	a.audit.Record(r, "node", "firewall_toggle", "node", strconv.FormatInt(n.ID, 10), audit.ResultSuccess,
		audit.DetailJSON(map[string]any{"enabled": req.Enabled}))
	writeJSON(w, map[string]string{"status": "ok"})
}
