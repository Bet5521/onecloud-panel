package apps

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"onecloud-panel/internal/recipes"
	"onecloud-panel/internal/store"
)

// 容器统一命名前缀。
const containerPrefix = "ocp-"

func containerName(appID string) string { return containerPrefix + strings.ReplaceAll(appID, "_", "-") }

// engineArchGO 面板架构 → Engine image inspect 的 Architecture 值。
var engineArchGO = map[string]string{
	"armv7l":  "arm",
	"aarch64": "arm64",
	"x86_64":  "amd64",
	"i386":    "386",
}

// ---- 请求/响应体 ----

type createContainer struct {
	Image        string              `json:"Image"`
	Env          []string            `json:"Env,omitempty"`
	ExposedPorts map[string]struct{} `json:"ExposedPorts"`
	HostConfig   hostConfig          `json:"HostConfig"`
}

type hostConfig struct {
	PortBindings  map[string][]portBinding `json:"PortBindings,omitempty"`
	Binds         []string                 `json:"Binds,omitempty"`
	Privileged    bool                     `json:"Privileged,omitempty"`
	NetworkMode   string                   `json:"NetworkMode,omitempty"`
	RestartPolicy restartPolicy            `json:"RestartPolicy,omitempty"`
}

type portBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

type restartPolicy struct {
	Name string `json:"Name"`
}

type createdContainer struct {
	ID       string   `json:"Id"`
	Warnings []string `json:"Warnings"`
}

type containerState struct {
	Status  string `json:"Status"`
	Running bool   `json:"Running"`
}

type containerInspect struct {
	ID    string         `json:"Id"`
	Name  string         `json:"Name"`
	State containerState `json:"State"`
}

type imageInspect struct {
	ID           string `json:"Id"`
	Architecture string `json:"Architecture"`
	OS           string `json:"Os"`
}

type containerListItem struct {
	ID    string   `json:"Id"`
	Names []string `json:"Names"`
	State string   `json:"State"`
}

// ---- 安装 ----

// DockerInstall 容器方式安装（幂等：同名容器存在即拒绝）。
func (m *Manager) DockerInstall(ctx context.Context, w io.Writer,
	nodeID int64, recipeID string, vars map[string]string) error {

	n, recipe, rendered, ex, err := m.prepare(nodeID, recipeID, "docker", vars)
	if err != nil {
		return err
	}
	if err := validateVariables(recipe, vars); err != nil {
		return err
	}
	ds := rendered.Recipe.Docker
	eng, err := m.EngineFor(n)
	if err != nil {
		return err
	}

	if in, err := m.store.GetInstallation(nodeID, recipeID); err == nil && in != nil {
		return fmt.Errorf("该应用已安装（%s），请先卸载", in.Status)
	}

	cname := containerName(recipeID)
	if existing, err := findContainer(ctx, eng, cname); err == nil && existing != "" {
		return fmt.Errorf("同名容器 %s 已存在，请先卸载", cname)
	}

	// 拉取镜像（镜像已存在时 Engine 立即返回）
	imageRef := strings.TrimSpace(ds.Image)
	if err := pullImage(ctx, w, eng, imageRef); err != nil {
		return err
	}
	// 架构不匹配拦截（在建容器之前，避免留下半成品）
	if want := engineArchGO[n.Arch]; want != "" {
		ii, err := inspectImage(ctx, eng, imageRef)
		if err == nil && ii.Architecture != "" && ii.Architecture != want {
			return fmt.Errorf("镜像架构 %s 与节点 %s 不匹配（需 %s）",
				ii.Architecture, n.Arch, want)
		}
	}

	// 前置步骤（目录等，走执行器）
	if err := m.runSteps(ctx, w, ex, ds.InstallSteps); err != nil {
		return err
	}

	// 组装创建参数
	body, err := buildCreateBody(imageRef, ds, rendered)
	if err != nil {
		return err
	}
	q := url.Values{"name": []string{cname}}
	resp, err := eng.Do(ctx, "POST", "/containers/create", q,
		bytes.NewReader(body), "application/json")
	if err != nil {
		return fmt.Errorf("创建容器失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("创建容器返回 %d: %s", resp.StatusCode, tail(string(data), 300))
	}
	var created createdContainer
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return err
	}
	fmt.Fprintf(w, "→ 容器已创建 %s（%s）\n", cname, shortID(created.ID))

	// 启动
	if err := containerPOST(ctx, eng, created.ID, "start", nil); err != nil {
		return err
	}
	fmt.Fprintf(w, "→ 容器已启动\n")

	// 健康检查
	if rendered.Recipe.Healthcheck != nil {
		if err := m.waitHealthy(ctx, w, ex, rendered.Recipe.Healthcheck); err != nil {
			if _, cerr := m.store.CreateInstallation(&store.AppInstallation{
				NodeID: nodeID, AppID: recipeID, Method: "docker",
				Status: "error", ContainerID: created.ID, ContainerName: cname,
				Params: paramsJSON(vars),
			}); cerr != nil {
				fmt.Fprintf(w, "  警告: 异常安装记录写入失败: %v\n", cerr)
			}
			return err
		}
	} else {
		time.Sleep(500 * time.Millisecond)
	}

	if _, err := m.store.CreateInstallation(&store.AppInstallation{
		NodeID: nodeID, AppID: recipeID, Method: "docker",
		Status: "installed", ContainerID: created.ID,
		ContainerName: cname, Params: paramsJSON(vars),
	}); err != nil {
		return err
	}
	fmt.Fprintf(w, "✓ %s 容器安装完成（节点：%s）\n", recipe.Name, n.Name)
	return nil
}

// buildCreateBody 将 DockerSpec 翻译为 Engine create JSON。
func buildCreateBody(imageRef string, ds *recipes.DockerSpec,
	rendered *recipes.RenderedRecipe) ([]byte, error) {
	cc := createContainer{
		Image:        imageRef,
		Env:          ds.Env,
		ExposedPorts: map[string]struct{}{},
		HostConfig: hostConfig{
			Binds:       ds.Volumes,
			Privileged:  ds.Privileged,
			NetworkMode: ds.NetworkMode,
			RestartPolicy: restartPolicy{
				Name: defaultStr(ds.RestartPolicy, "unless-stopped"),
			},
		},
	}
	if cc.HostConfig.NetworkMode == "" {
		cc.HostConfig.NetworkMode = "bridge"
	}
	pb := map[string][]portBinding{}
	for _, p := range ds.Ports {
		hostPort, ctrPort, proto, err := parsePortSpec(p)
		if err != nil {
			return nil, err
		}
		key := ctrPort + "/" + proto
		cc.ExposedPorts[key] = struct{}{}
		if hostPort != "" {
			pb[key] = []portBinding{{HostPort: hostPort}}
		}
	}
	if len(pb) > 0 {
		cc.HostConfig.PortBindings = pb
	}
	return json.Marshal(cc)
}

// parsePortSpec 解析 "8080:80/tcp"（proto 默认 tcp）。
func parsePortSpec(s string) (hostPort, ctrPort, proto string, err error) {
	proto = "tcp"
	if i := strings.IndexByte(s, '/'); i >= 0 {
		proto = s[i+1:]
		s = s[:i]
	}
	parts := strings.Split(s, ":")
	switch len(parts) {
	case 1:
		ctrPort = parts[0]
	case 2:
		hostPort, ctrPort = parts[0], parts[1]
	default:
		return "", "", "", fmt.Errorf("端口格式非法 %q（应为 主机:容器/协议）", s)
	}
	if _, err := strconv.Atoi(ctrPort); err != nil {
		return "", "", "", fmt.Errorf("容器端口非法 %q", ctrPort)
	}
	if hostPort != "" {
		if _, err := strconv.Atoi(hostPort); err != nil {
			return "", "", "", fmt.Errorf("主机端口非法 %q", hostPort)
		}
	}
	if proto != "tcp" && proto != "udp" {
		return "", "", "", fmt.Errorf("协议非法 %q", proto)
	}
	return hostPort, ctrPort, proto, nil
}

// pullImage 拉取镜像并解析进度流。
func pullImage(ctx context.Context, w io.Writer, eng interface {
	Do(context.Context, string, string, url.Values, io.Reader, string) (*http.Response, error)
}, ref string) error {
	// tag 为最后一个 ':' 之后的部分（须在最后一个 '/' 之后，避免误伤 registry 端口）
	repo, tag := ref, "latest"
	lastSlash := strings.LastIndex(ref, "/")
	if lastColon := strings.LastIndex(ref, ":"); lastColon > lastSlash {
		repo, tag = ref[:lastColon], ref[lastColon+1:]
	}
	q := url.Values{"fromImage": []string{repo}, "tag": []string{tag}}
	fmt.Fprintf(w, "→ 拉取镜像 %s:%s\n", repo, tag)

	resp, err := eng.Do(ctx, "POST", "/images/create", q, nil, "")
	if err != nil {
		return fmt.Errorf("镜像拉取失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("镜像拉取返回 %d: %s", resp.StatusCode, tail(string(data), 300))
	}

	lastStatus := ""
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var msg struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if msg.Error != "" {
			return fmt.Errorf("镜像拉取错误: %s", msg.Error)
		}
		if msg.Status != "" && msg.Status != lastStatus {
			fmt.Fprintf(w, "  %s\n", msg.Status)
			lastStatus = msg.Status
		}
	}
	return scanner.Err()
}

func inspectImage(ctx context.Context, eng interface {
	Do(context.Context, string, string, url.Values, io.Reader, string) (*http.Response, error)
}, ref string) (*imageInspect, error) {
	path := "/images/" + url.PathEscape(ref) + "/json"
	resp, err := eng.Do(ctx, "GET", path, nil, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("image inspect %d", resp.StatusCode)
	}
	var ii imageInspect
	return &ii, json.NewDecoder(resp.Body).Decode(&ii)
}

func findContainer(ctx context.Context, eng interface {
	Do(context.Context, string, string, url.Values, io.Reader, string) (*http.Response, error)
}, name string) (string, error) {
	// Engine filters 为 JSON
	filterJSON, _ := json.Marshal(map[string][]string{"name": {name}})
	q := url.Values{"all": []string{"1"}, "filters": []string{string(filterJSON)}}
	resp, err := eng.Do(ctx, "GET", "/containers/json", q, nil, "")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("containers %d", resp.StatusCode)
	}
	var items []containerListItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return "", err
	}
	want := "/" + name
	for _, it := range items {
		for _, nm := range it.Names {
			if nm == want {
				return it.ID, nil
			}
		}
	}
	return "", nil
}

// containerRunning 读取容器 Running 标志。
func containerRunning(ctx context.Context, eng interface {
	Do(context.Context, string, string, url.Values, io.Reader, string) (*http.Response, error)
}, id string) (bool, error) {
	resp, err := eng.Do(ctx, "GET", "/containers/"+id+"/json", nil, nil, "")
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false, fmt.Errorf("inspect %d", resp.StatusCode)
	}
	var ci containerInspect
	if err := json.NewDecoder(resp.Body).Decode(&ci); err != nil {
		return false, err
	}
	return ci.State.Running, nil
}

// containerPOST start/stop/restart。
func containerPOST(ctx context.Context, eng interface {
	Do(context.Context, string, string, url.Values, io.Reader, string) (*http.Response, error)
}, id, action string, q url.Values) error {
	resp, err := eng.Do(ctx, "POST", "/containers/"+id+"/"+action, q, nil, "")
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 && resp.StatusCode != 304 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s 返回 %d: %s", action, resp.StatusCode, tail(string(data), 200))
	}
	return nil
}

// ---- 卸载 ----

// DockerUninstall 停止并删除容器；purgeData=true 删除命名数据卷（镜像保留）。
func (m *Manager) DockerUninstall(ctx context.Context, w io.Writer,
	nodeID int64, recipeID string, purgeData bool) error {

	in, err := m.store.GetInstallation(nodeID, recipeID)
	if err != nil {
		return fmt.Errorf("应用未安装")
	}
	n, recipe, rendered, _, err := m.prepare(nodeID, recipeID, "docker", installationVars(in))
	if err != nil {
		return err
	}
	eng, err := m.EngineFor(n)
	if err != nil {
		return err
	}
	cid := in.ContainerID
	if cid == "" {
		id, ferr := findContainer(ctx, eng, in.ContainerName)
		if ferr != nil || id == "" {
			fmt.Fprintln(w, "→ 容器已不存在，仅清理记录")
			return m.store.DeleteInstallation(nodeID, recipeID)
		}
		cid = id
	}

	fmt.Fprintf(w, "→ 停止容器 %s\n", in.ContainerName)
	_ = containerPOST(ctx, eng, cid, "stop", url.Values{"t": []string{"10"}})

	fmt.Fprintf(w, "→ 删除容器\n")
	resp, err := eng.Do(ctx, "DELETE", "/containers/"+cid,
		url.Values{"force": []string{"1"}}, nil, "")
	if err != nil {
		return err
	}
	resp.Body.Close()

	// 卸载步骤
	if len(rendered.Recipe.Docker.UninstallSteps) > 0 {
		ex, _ := m.ExecutorFor(n)
		if err := m.runSteps(ctx, w, ex, rendered.Recipe.Docker.UninstallSteps); err != nil {
			return err
		}
	}

	if purgeData {
		for _, v := range rendered.Recipe.Docker.Volumes {
			name := strings.SplitN(v, ":", 2)[0]
			if strings.HasPrefix(name, "/") {
				fmt.Fprintf(w, "→ 保留绑定目录 %s（不自动删除宿主机目录）\n", name)
				continue
			}
			fmt.Fprintf(w, "→ 删除命名卷 %s\n", name)
			rr, rerr := eng.Do(ctx, "DELETE", "/volumes/"+url.PathEscape(name),
				url.Values{"force": []string{"1"}}, nil, "")
			if rerr == nil {
				rr.Body.Close()
			}
		}
	} else {
		fmt.Fprintln(w, "→ 已保留数据卷")
	}
	fmt.Fprintln(w, "→ 镜像保留在本地缓存")

	if err := m.store.DeleteInstallation(nodeID, recipeID); err != nil {
		return err
	}
	fmt.Fprintf(w, "✓ %s 容器已卸载（节点：%s）\n", recipe.Name, n.Name)
	return nil
}

// ---- 动作/状态/日志 ----

func (m *Manager) dockerServiceAction(ctx context.Context,
	nodeID int64, in *store.AppInstallation, action string) (string, error) {
	n, err := m.store.GetNode(nodeID)
	if err != nil {
		return "", err
	}
	eng, err := m.EngineFor(n)
	if err != nil {
		return "", err
	}
	cid := in.ContainerID
	if cid == "" {
		id, _ := findContainer(ctx, eng, in.ContainerName)
		cid = id
	}
	if cid == "" {
		return "", fmt.Errorf("容器不存在")
	}
	if err := containerPOST(ctx, eng, cid, action, nil); err != nil {
		return "", err
	}
	return in.ContainerName, nil
}

func (m *Manager) dockerStatus(ctx context.Context,
	nodeID int64, recipeID string, in *store.AppInstallation) (map[string]any, error) {
	n, recipe, _, ex, err := m.prepare(nodeID, recipeID, "docker", installationVars(in))
	if err != nil {
		return nil, err
	}
	eng, err := m.EngineFor(n)
	if err != nil {
		return nil, err
	}
	cid := in.ContainerID
	if cid == "" {
		id, _ := findContainer(ctx, eng, in.ContainerName)
		cid = id
	}
	res := map[string]any{
		"container_id":   cid,
		"container_name": in.ContainerName,
		"method":         "docker",
		"status":         in.Status,
	}
	if cid != "" {
		resp, err := eng.Do(ctx, "GET", "/containers/"+cid+"/json", nil, nil, "")
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == 200 {
				var ci containerInspect
				if json.NewDecoder(resp.Body).Decode(&ci) == nil {
					res["running"] = ci.State.Running
					res["state"] = ci.State.Status
				}
			}
		}
	}
	if recipe.Healthcheck != nil {
		ok, detail := m.probe(ctx, ex, recipe.Healthcheck)
		res["healthy"] = ok
		res["health_detail"] = detail
	}
	return res, nil
}

func (m *Manager) dockerJournal(ctx context.Context,
	nodeID int64, in *store.AppInstallation, lines int) (string, error) {
	n, err := m.store.GetNode(nodeID)
	if err != nil {
		return "", err
	}
	eng, err := m.EngineFor(n)
	if err != nil {
		return "", err
	}
	cid := in.ContainerID
	if cid == "" {
		id, _ := findContainer(ctx, eng, in.ContainerName)
		cid = id
	}
	if cid == "" {
		return "", fmt.Errorf("容器不存在")
	}
	q := url.Values{
		"stdout": []string{"1"}, "stderr": []string{"1"},
		"tail": []string{strconv.Itoa(lines)},
	}
	resp, err := eng.Do(ctx, "GET", "/containers/"+cid+"/logs", q, nil, "")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		data, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("logs %d: %s", resp.StatusCode, tail(string(data), 200))
	}
	return demuxLogs(resp.Body)
}

// demuxLogs 解析非 TTY 容器日志（8 字节头 + payload）。
func demuxLogs(r io.Reader) (string, error) {
	var out bytes.Buffer
	header := make([]byte, 8)
	for {
		if _, err := io.ReadFull(r, header); err != nil {
			if err == io.EOF {
				return out.String(), nil
			}
			if err == io.ErrUnexpectedEOF {
				return out.String(), nil
			}
			return "", err
		}
		n := binary.BigEndian.Uint32(header[4:8])
		if n == 0 {
			continue
		}
		if _, err := io.CopyN(&out, r, int64(n)); err != nil {
			return out.String(), err
		}
	}
}

// ---- 小工具 ----

func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
