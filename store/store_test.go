package store

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"testing"
	"time"

	"go-agentops/cache"
	"go-agentops/internal/testutil"
	"go-agentops/model"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
)

// mr 是全局的 miniredis 实例，测试里用它检查缓存 key、模拟 Redis 宕机
var mr *miniredis.Miniredis

func TestMain(m *testing.M) {
	cfg, err := testutil.NewTestConfig("agentops_test_store")
	if err != nil {
		log.Printf("跳过：%v（启动方式：cd go-agentops && docker compose up -d --wait）", err)
		os.Exit(0)
	}
	if err := Init(cfg); err != nil {
		log.Fatalf("store.Init 失败: %v", err)
	}
	defer Close()

	mr, err = miniredis.Run()
	if err != nil {
		log.Fatalf("miniredis 启动失败: %v", err)
	}
	defer mr.Close()
	cfg.RedisAddr = mr.Addr()
	if err := cache.Init(cfg); err != nil {
		log.Fatalf("cache.Init 失败: %v", err)
	}
	defer cache.Close()

	os.Exit(m.Run())
}

func makePrompt(t *testing.T, name string) *model.Prompt {
	t.Helper()
	p, err := CreatePrompt(name, "hello {{input}}")
	if err != nil {
		t.Fatalf("CreatePrompt 失败: %v", err)
	}
	return p
}

func makeTrace(t *testing.T) *model.Trace {
	t.Helper()
	tr := &model.Trace{
		ID:          uuid.New().String(),
		SessionID:   "s001",
		UID:         "uid_test",
		Model:       "deepseek-pro",
		Prompt:      "hello",
		Response:    "resp-original",
		TotalTokens: 100,
		LatencyMs:   200,
		Status:      0,
		CreatedAt:   time.Now().UTC(),
	}
	if err := CreateTrace(tr); err != nil {
		t.Fatalf("CreateTrace 失败: %v", err)
	}
	return tr
}

// TestCreateVersionByID_ConcurrentUniqueVer 并发建版本：10 个 goroutine 同时建版本，
// 版本号必须互不重复且恰好是 {2..11}。
// 旧实现（COUNT+1 无事务）在这里必挂——这是"事务 + FOR UPDATE 行锁"的回归测试，
// 用 go test -race 跑还能验证无数据竞争。
func TestCreateVersionByID_ConcurrentUniqueVer(t *testing.T) {
	p := makePrompt(t, "concurrent")
	const n = 10
	vers := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := CreateVersionByID(p.ID, fmt.Sprintf("content-%d", i))
			if err != nil {
				t.Errorf("并发建版本失败: %v", err)
				return
			}
			vers[i] = v.Ver
		}(i)
	}
	wg.Wait()

	seen := make(map[int]bool, n)
	for _, v := range vers {
		if v < 2 || v > n+1 {
			t.Errorf("版本号越界: %d", v)
		}
		if seen[v] {
			t.Errorf("版本号重复: %d", v)
		}
		seen[v] = true
	}
	if len(seen) != n {
		t.Errorf("期望 %d 个互不相同的版本号，实际只有 %d 个", n, len(seen))
	}
}

func TestUpdateVersion_Idempotent(t *testing.T) {
	p := makePrompt(t, "idempotent")
	if err := UpdateVersion(p.ID, 1); err != nil {
		t.Fatalf("第一次 PUT 失败: %v", err)
	}
	// 重复 PUT：cur_ver 已经是 1，MySQL 默认 RowsAffected=0 会误判 404，
	// DSN 里 clientFoundRows=true 使其返回匹配行数 1
	if err := UpdateVersion(p.ID, 1); err != nil {
		t.Fatalf("重复 PUT 同一版本应成功（clientFoundRows），实际: %v", err)
	}
}

func TestUpdateVersion_VersionNotExist(t *testing.T) {
	p := makePrompt(t, "no-ver")
	if err := UpdateVersion(p.ID, 99); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("期望 sql.ErrNoRows，实际: %v", err)
	}
}

func TestCreateVersionByID_PromptNotExist(t *testing.T) {
	_, err := CreateVersionByID(uuid.New().String(), "content")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("期望 sql.ErrNoRows，实际: %v", err)
	}
}

// TestGetTraceByID_CacheAside 验证三段式：回源回填 → 命中；
// 直接绕过缓存改库后仍读到旧值，证明第二次确实走的缓存
func TestGetTraceByID_CacheAside(t *testing.T) {
	tr := makeTrace(t)

	got, err := GetTraceByID(tr.ID) // 第一次：miss → 查 MySQL → 回填
	if err != nil {
		t.Fatalf("第一次 GetTraceByID 失败: %v", err)
	}
	if got.Response != "resp-original" {
		t.Fatalf("期望 resp-original，实际 %s", got.Response)
	}
	if !mr.Exists(cacheKeyTrace(tr.ID)) {
		t.Fatal("期望缓存已回填，但 key 不存在")
	}

	// 绕过缓存直接改 MySQL
	if _, err := db.Exec(`UPDATE traces SET response = 'resp-hacked' WHERE id = ?`, tr.ID); err != nil {
		t.Fatalf("直接改库失败: %v", err)
	}

	got, err = GetTraceByID(tr.ID) // 第二次：命中缓存，看不到库里的修改
	if err != nil {
		t.Fatalf("第二次 GetTraceByID 失败: %v", err)
	}
	if got.Response != "resp-original" {
		t.Errorf("第二次应命中缓存返回旧值 resp-original，实际 %s", got.Response)
	}
}

// TestGetPromptByID_InvalidatedOnCreateVersion 写后失效：
// 缓存回填后建新版本，再读必须看到新版本列表
func TestGetPromptByID_InvalidatedOnCreateVersion(t *testing.T) {
	p := makePrompt(t, "inval-create")

	_, vers, err := GetPromptByID(p.ID) // 回填缓存（此时只有 1 个版本）
	if err != nil {
		t.Fatalf("GetPromptByID 失败: %v", err)
	}
	if len(vers) != 1 {
		t.Fatalf("初始版本数期望 1，实际 %d", len(vers))
	}
	if !mr.Exists(cacheKeyPrompt(p.ID)) {
		t.Fatal("期望缓存已回填")
	}

	if _, err := CreateVersionByID(p.ID, "v2"); err != nil {
		t.Fatalf("CreateVersionByID 失败: %v", err)
	}

	_, vers, err = GetPromptByID(p.ID) // 缓存被 DEL，回源看到 2 个版本
	if err != nil {
		t.Fatalf("GetPromptByID 失败: %v", err)
	}
	if len(vers) != 2 {
		t.Errorf("期望读到 2 个版本（缓存失效生效），实际 %d", len(vers))
	}
}

// TestGetPromptByID_InvalidatedOnUpdateVersion 切换版本后 cur_ver 立即反映
func TestGetPromptByID_InvalidatedOnUpdateVersion(t *testing.T) {
	p := makePrompt(t, "inval-update")
	if _, err := CreateVersionByID(p.ID, "v2"); err != nil { // 建 v2（顺带删了一次缓存）
		t.Fatalf("CreateVersionByID 失败: %v", err)
	}
	if _, _, err := GetPromptByID(p.ID); err != nil { // 回填缓存（cur_ver 仍是 1）
		t.Fatalf("GetPromptByID 失败: %v", err)
	}

	if err := UpdateVersion(p.ID, 2); err != nil {
		t.Fatalf("UpdateVersion 失败: %v", err)
	}

	got, _, err := GetPromptByID(p.ID)
	if err != nil {
		t.Fatalf("GetPromptByID 失败: %v", err)
	}
	if got.CurVer != 2 {
		t.Errorf("cur_ver 期望 2（缓存失效生效），实际 %d", got.CurVer)
	}
}

// TestGetTraceByID_RedisDownFallback 降级核心用例：
// Redis 挂掉后 GetTraceByID 必须还能返回数据（直查 MySQL），不能报错
func TestGetTraceByID_RedisDownFallback(t *testing.T) {
	tr := makeTrace(t)
	if _, err := GetTraceByID(tr.ID); err != nil { // 先回填一次缓存
		t.Fatalf("GetTraceByID 失败: %v", err)
	}

	mr.Close() // 模拟 Redis 宕机

	got, err := GetTraceByID(tr.ID)
	if err != nil {
		t.Fatalf("Redis 挂掉后应降级直查 MySQL，实际报错: %v", err)
	}
	if got.Response != "resp-original" {
		t.Errorf("降级路径数据错误: %s", got.Response)
	}
}
