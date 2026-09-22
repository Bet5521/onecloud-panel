package agentclient

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"onecloud-panel/internal/agent"
)

// TestWriteReadFileBinaryRoundTrip 契约测试：客户端必须经 base64 传输二进制，
// 且能无损还原服务端以 base64 返回的内容。
//
// 历史 bug：客户端曾用 agent.FileReq{Content: string(data)} 直接承载二进制，
// encoding/json 会把非法 UTF-8 字节替换为 U+FFFD（EF BF BD），导致推送到节点的
// 二进制损坏、体积膨胀。真机表现为自定义应用 systemd 单元启动即
// "Exec format error"（status=203/EXEC）。
func TestWriteReadFileBinaryRoundTrip(t *testing.T) {
	var stored []byte
	var sawB64 bool

	mux := http.NewServeMux()
	mux.HandleFunc("PUT /v1/file", func(w http.ResponseWriter, r *http.Request) {
		var req agent.FileReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// 契约：二进制写入必须走 content_b64，不得用文本字段承载二进制
		if req.ContentB64 == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"content_b64 required"}`))
			return
		}
		sawB64 = true
		dec, err := base64.StdEncoding.DecodeString(req.ContentB64)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		stored = dec
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /v1/file", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(agent.FileResp{
			Path:       r.URL.Query().Get("path"),
			ContentB64: base64.StdEncoding.EncodeToString(stored),
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "tok")

	// 含 ELF 魔数与多种非法 UTF-8 字节序列的载荷
	payload := append([]byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00},
		0xff, 0xfe, 0x80, 0xc3, 0x28, 0xed, 0xa0, 0x80, 0xf4, 0x90, 0x80, 0x80)

	if err := c.WriteFile("/opt/onecloud-apps/1/bin", payload); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !sawB64 {
		t.Fatal("客户端未使用 content_b64 传输二进制")
	}
	if !bytes.Equal(stored, payload) {
		t.Fatalf("写入内容不一致: got %d bytes, want %d bytes", len(stored), len(payload))
	}

	got, err := c.ReadFile("/opt/onecloud-apps/1/bin")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("读取内容不一致: got %d bytes, want %d bytes", len(got), len(payload))
	}
}
