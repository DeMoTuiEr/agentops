package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go-agentops/cache"
	"go-agentops/config"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
)

// setup 起 miniredis + 挂好限流中间件的 gin 引擎（纯内存测试，不依赖 MySQL/Redis）
func setup(t *testing.T, limit int, window time.Duration) (*miniredis.Miniredis, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis 启动失败: %v", err)
	}
	cfg := &config.Config{RedisAddr: mr.Addr()}
	if err := cache.Init(cfg); err != nil {
		t.Fatalf("cache.Init 失败: %v", err)
	}
	r := gin.New()
	r.Use(RateLimit(limit, window))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })
	return mr, r
}

func req(t *testing.T, r *gin.Engine, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/", nil)
	if remoteAddr != "" {
		request.RemoteAddr = remoteAddr
	}
	r.ServeHTTP(w, request)
	return w
}

func TestRateLimit_AllowWithinLimit(t *testing.T) {
	_, r := setup(t, 3, time.Minute)

	for i, want := range []string{"2", "1", "0"} {
		w := req(t, r, "")
		if w.Code != http.StatusOK {
			t.Fatalf("第 %d 次请求期望 200，实际 %d", i+1, w.Code)
		}
		if got := w.Header().Get("X-RateLimit-Remaining"); got != want {
			t.Errorf("第 %d 次请求 Remaining 期望 %s，实际 %s", i+1, want, got)
		}
	}
}

func TestRateLimit_BlockOverLimit(t *testing.T) {
	_, r := setup(t, 3, time.Minute)
	for i := 0; i < 3; i++ {
		if w := req(t, r, ""); w.Code != http.StatusOK {
			t.Fatalf("第 %d 次请求期望 200，实际 %d", i+1, w.Code)
		}
	}
	if w := req(t, r, ""); w.Code != http.StatusTooManyRequests {
		t.Fatalf("第 4 次请求期望 429，实际 %d", w.Code)
	}
}

// TestRateLimit_WindowReset 用 miniredis.FastForward 快进时钟验证窗口过期后计数归零，
// 不需要真的 sleep 一分钟
func TestRateLimit_WindowReset(t *testing.T) {
	mr, r := setup(t, 2, time.Minute)
	req(t, r, "")
	req(t, r, "")
	if w := req(t, r, ""); w.Code != http.StatusTooManyRequests {
		t.Fatalf("窗口内第 3 次期望 429，实际 %d", w.Code)
	}

	mr.FastForward(time.Minute + time.Second) // 窗口过期

	if w := req(t, r, ""); w.Code != http.StatusOK {
		t.Fatalf("窗口过期后期望 200，实际 %d", w.Code)
	}
}

// TestRateLimit_RedisDownFailOpen 降级策略：Redis 挂掉时放行而不是误杀
func TestRateLimit_RedisDownFailOpen(t *testing.T) {
	mr, r := setup(t, 1, time.Minute)
	req(t, r, "")

	mr.Close() // 模拟 Redis 宕机

	if w := req(t, r, ""); w.Code != http.StatusOK {
		t.Fatalf("Redis 挂掉应 fail-open 放行，实际 %d", w.Code)
	}
}

// TestRateLimit_KeyByIP 不同 IP 各自独立计数
func TestRateLimit_KeyByIP(t *testing.T) {
	_, r := setup(t, 1, time.Minute)

	if w := req(t, r, "10.0.0.1:1234"); w.Code != http.StatusOK {
		t.Fatalf("IP A 第一次期望 200，实际 %d", w.Code)
	}
	if w := req(t, r, "10.0.0.1:1234"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("IP A 第二次期望 429，实际 %d", w.Code)
	}
	if w := req(t, r, "10.0.0.2:1234"); w.Code != http.StatusOK {
		t.Fatalf("IP B 第一次期望 200（独立计数），实际 %d", w.Code)
	}
}
