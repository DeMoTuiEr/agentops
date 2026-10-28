package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"go-agentops/cache"
	"go-agentops/handler"
	"go-agentops/internal/testutil"
	"go-agentops/model"
	"go-agentops/store"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var router *gin.Engine

func cmp(t *testing.T, got, want, name string) {
	t.Helper()
	if got != want {
		t.Errorf("%s : got %s , want %s", name, got, want)
	}
}

// TestMain 是所有测试的统一入口：
//   - MySQL 不可达 → 打印跳过并 os.Exit(0)（集成测试不因环境缺失而红）；
//   - Redis 用 miniredis 内存替身，缓存路径照常工作，不需要真实 Redis。
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)

	cfg, err := testutil.NewTestConfig("agentops_test_main")
	if err != nil {
		log.Printf("跳过：%v（启动方式：cd go-agentops && docker compose up -d --wait）", err)
		os.Exit(0)
	}
	if err := store.Init(cfg); err != nil {
		log.Fatalf("store.Init 失败: %v", err)
	}
	defer store.Close()

	mr, err := miniredis.Run()
	if err != nil {
		log.Fatalf("miniredis 启动失败: %v", err)
	}
	defer mr.Close()
	cfg.RedisAddr = mr.Addr()
	if err := cache.Init(cfg); err != nil {
		log.Fatalf("cache.Init 失败: %v", err)
	}
	defer cache.Close()

	router = gin.Default()
	router.POST("/api/v1/traces", handler.CreateTrace)
	router.GET("/api/v1/traces/:id", handler.GetTrace)
	router.GET("/api/v1/traces", handler.GetTraceList)
	router.POST("/api/v1/prompts", handler.CreatePrompt)
	router.GET("/api/v1/prompts/:id", handler.GetPromptByID)
	router.POST("/api/v1/prompts/:id/versions", handler.CreateVersionByID)
	router.PUT("/api/v1/prompts/:id/versions/:ver", handler.UpdateVersion)

	os.Exit(m.Run())
}

func doReq(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func createTrace(t *testing.T, uid string) model.Trace {
	t.Helper()
	body := fmt.Sprintf(`{"session_id":"s001","uid":%q,"model":"deepseek-pro","prompt":"hello","response":"hi there","total_tokens":100,"latency_ms":200,"status":1}`, uid)
	w := doReq(t, "POST", "/api/v1/traces", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create trace: expected 201, got %d, body=%s", w.Code, w.Body.String())
	}
	var tr model.Trace
	if err := json.Unmarshal(w.Body.Bytes(), &tr); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	return tr
}

// TestCreateAndGetTrace 保留迁移前的全链路用例，额外断言 CreatedAt 是有效时间
func TestCreateAndGetTrace(t *testing.T) {
	tr := createTrace(t, "elysia_miao")

	w := doReq(t, "GET", "/api/v1/traces/"+tr.ID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var got model.Trace
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	cmp(t, got.UID, "elysia_miao", "UID")
	cmp(t, got.Model, "deepseek-pro", "Model")
	cmp(t, got.Prompt, "hello", "Prompt")
	cmp(t, got.Response, "hi there", "Response")
	if got.TotalTokens != 100 {
		t.Errorf("total_tokens: got %d, want 100", got.TotalTokens)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt 不该是零值（MySQL DATETIME → time.Time 映射失败？）")
	}
}

func TestGetTrace_NotFound(t *testing.T) {
	w := doReq(t, "GET", "/api/v1/traces/"+uuid.New().String(), "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// TestGetTraceList_FilterAndPaginate 覆盖 DATETIME 排序 + LIMIT/OFFSET 绑定 + 过滤
func TestGetTraceList_FilterAndPaginate(t *testing.T) {
	createTrace(t, "user_a")
	createTrace(t, "user_a")
	createTrace(t, "user_b")

	w := doReq(t, "GET", "/api/v1/traces?uid=user_a&page=1&page_size=10", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var list []model.Trace
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("期望过滤出 2 条，实际 %d", len(list))
	}
	for _, tr := range list {
		if tr.UID != "user_a" {
			t.Errorf("过滤失效：混入了 %s 的 trace", tr.UID)
		}
	}

	// 分页：page=1 page_size=1 只返回 1 条
	w = doReq(t, "GET", "/api/v1/traces?page=1&page_size=1", "")
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("期望分页返回 1 条，实际 %d", len(list))
	}
}

// TestGetTraceList_PageZeroClamped MySQL 预处理对负数 LIMIT/OFFSET 报 errno 1210，
// 迁移前 ?page=0 会 500，现在应被夹紧成 200
func TestGetTraceList_PageZeroClamped(t *testing.T) {
	w := doReq(t, "GET", "/api/v1/traces?page=0&page_size=10", "")
	if w.Code != http.StatusOK {
		t.Fatalf("?page=0 期望 200，实际 %d, body=%s", w.Code, w.Body.String())
	}
	w = doReq(t, "GET", "/api/v1/traces?page=1&page_size=9999", "")
	if w.Code != http.StatusOK {
		t.Fatalf("page_size 越界期望 200，实际 %d", w.Code)
	}
}

// TestPrompt_FullLifecycle prompt 建→建版本→切版本 全链路。
// 重复 PUT 同一版本号必须仍 200：这是 clientFoundRows=true 的回归测试
// （MySQL 默认 RowsAffected 返回"改变行数"，重复 PUT 会误判 404）。
func TestPrompt_FullLifecycle(t *testing.T) {
	w := doReq(t, "POST", "/api/v1/prompts", `{"name":"p1","content":"hello {{input}}"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create prompt: expected 201, got %d, body=%s", w.Code, w.Body.String())
	}
	var p model.Prompt
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if p.CurVer != 1 {
		t.Fatalf("初始 cur_ver 期望 1，实际 %d", p.CurVer)
	}

	// 第一次 GET 会回填缓存；后续写操作要删缓存，最终读到的必须是新数据
	w = doReq(t, "GET", "/api/v1/prompts/"+p.ID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("get prompt: expected 200, got %d", w.Code)
	}

	w = doReq(t, "POST", "/api/v1/prompts/"+p.ID+"/versions", `{"content":"v2 content"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create version: expected 201, got %d, body=%s", w.Code, w.Body.String())
	}
	var pv model.PromptVersion
	if err := json.Unmarshal(w.Body.Bytes(), &pv); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if pv.Ver != 2 {
		t.Fatalf("新版本号期望 2，实际 %d", pv.Ver)
	}

	// 切换 cur_ver 到 2
	w = doReq(t, "PUT", fmt.Sprintf("/api/v1/prompts/%s/versions/2", p.ID), "")
	if w.Code != http.StatusOK {
		t.Fatalf("update version: expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
	// 重复 PUT 同一 ver：clientFoundRows 回归点
	w = doReq(t, "PUT", fmt.Sprintf("/api/v1/prompts/%s/versions/2", p.ID), "")
	if w.Code != http.StatusOK {
		t.Fatalf("重复 PUT 同一版本期望 200（clientFoundRows），实际 %d, body=%s", w.Code, w.Body.String())
	}

	// 最终 GET：缓存已被删除重填，看到 cur_ver=2 和两个版本
	w = doReq(t, "GET", "/api/v1/prompts/"+p.ID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("get prompt: expected 200, got %d", w.Code)
	}
	var resp struct {
		Prompt   model.Prompt          `json:"prompt"`
		Versions []model.PromptVersion `json:"versions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.Prompt.CurVer != 2 {
		t.Errorf("cur_ver 期望 2，实际 %d", resp.Prompt.CurVer)
	}
	if len(resp.Versions) != 2 {
		t.Errorf("版本列表期望 2 个，实际 %d", len(resp.Versions))
	}
}
