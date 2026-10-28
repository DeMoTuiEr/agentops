package cache

import (
	"context"
	"testing"
	"time"

	"go-agentops/config"

	"github.com/alicebob/miniredis/v2"
)

// setup 起一个 miniredis 并 Init 缓存客户端（纯内存 Redis 替身，不依赖外部环境）
func setup(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis 启动失败: %v", err)
	}
	cfg := &config.Config{RedisAddr: mr.Addr()}
	if err := Init(cfg); err != nil {
		t.Fatalf("cache.Init 失败: %v", err)
	}
	return mr
}

type testVal struct {
	Name string `json:"name"`
	N    int    `json:"n"`
}

func TestGetSetJSON_RoundTrip(t *testing.T) {
	setup(t)
	ctx := context.Background()

	Set(ctx, "k1", testVal{Name: "hi", N: 42}, time.Minute)
	var got testVal
	if !Get(ctx, "k1", &got) {
		t.Fatal("期望命中")
	}
	if got.Name != "hi" || got.N != 42 {
		t.Errorf("反序列化结果错误: %+v", got)
	}
}

func TestGet_Miss(t *testing.T) {
	setup(t)
	var got testVal
	if Get(context.Background(), "no-such-key", &got) {
		t.Fatal("不存在的 key 应返回 miss")
	}
}

func TestDel(t *testing.T) {
	setup(t)
	ctx := context.Background()
	Set(ctx, "k1", "v", time.Minute)
	Del(ctx, "k1")
	var got string
	if Get(ctx, "k1", &got) {
		t.Fatal("Del 后应 miss")
	}
}

// TestGet_RedisDownReturnsMiss 降级语义：Redis 挂掉后 Get 返回 miss 且不 panic，
// Set/Del 静默失败不 panic——业务方拿到的永远是"可用的降级路径"
func TestGet_RedisDownReturnsMiss(t *testing.T) {
	mr := setup(t)
	ctx := context.Background()

	mr.Close() // 模拟 Redis 宕机

	var got testVal
	if Get(ctx, "k1", &got) {
		t.Fatal("Redis 挂掉后应返回 miss")
	}
	Set(ctx, "k1", testVal{Name: "x"}, time.Minute) // 不应 panic
	Del(ctx, "k1")                                  // 不应 panic
}

// TestIncrExpire 限流中间件依赖的两个命令
func TestIncrExpire(t *testing.T) {
	setup(t)
	ctx := context.Background()

	n1, err := Incr(ctx, "cnt")
	if err != nil || n1 != 1 {
		t.Fatalf("第一次 Incr 期望 1，实际 %d, err=%v", n1, err)
	}
	n2, err := Incr(ctx, "cnt")
	if err != nil || n2 != 2 {
		t.Fatalf("第二次 Incr 期望 2，实际 %d, err=%v", n2, err)
	}
	if err := Expire(ctx, "cnt", time.Minute); err != nil {
		t.Fatalf("Expire 失败: %v", err)
	}
}
