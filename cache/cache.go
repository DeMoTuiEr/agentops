// Package cache 封装 Redis 客户端。
//
// 设计原则（面试核心故事线）：
//  1. 缓存是"加速器"不是"生命线"——Redis 挂掉，业务必须继续跑（降级直查 MySQL）；
//  2. Get/Set/Del 吞掉所有错误：读不到就当 miss 回源，写失败只打日志；
//  3. Incr/Expire 把错误抛给调用方（限流中间件），由调用方决定降级策略（fail-open 放行）。
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync/atomic"
	"time"

	"go-agentops/config"

	"github.com/redis/go-redis/v9"
)

var rdb *redis.Client

// degraded 标记 Redis 当前是否处于故障状态。
// 用状态翻转日志（而不是每次失败都打日志）：Redis 挂掉期间每秒可能几百个请求，
// 每次都打会把日志刷爆、把磁盘写满。
var degraded atomic.Bool

// Init 创建客户端并 Ping 探活。返回错误时 main 只告警不退出——
// 见包注释第 1 条。
func Init(cfg *config.Config) error {
	rdb = redis.NewClient(&redis.Options{
		Addr:         cfg.RedisAddr,
		Password:     cfg.RedisPassword,
		DB:           cfg.RedisDB,
		DialTimeout:  500 * time.Millisecond, // 超时全部压短：Redis 故障时不能拖慢主链路
		ReadTimeout:  500 * time.Millisecond,
		WriteTimeout: 500 * time.Millisecond,
		PoolSize:     10,
		// 关闭命令重试：默认重试 3 次会让 Redis 宕机时每个请求都白等几秒，
		// 与"故障降级"目标矛盾——失败就该立刻 miss/回源，重试留给更上层的业务决策
		MaxRetries: -1,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		noteErr("ping", err)
		return err
	}
	noteOK()
	return nil
}

func Close() error {
	if rdb == nil {
		return nil
	}
	return rdb.Close()
}

// Enabled 报告缓存是否可用（未初始化或初始化失败 → 全部走降级路径）。
// main.go 的 /healthz 用它上报 Redis 状态。
func Enabled() bool { return rdb != nil }

// Get 从缓存反序列化。返回值 true=命中。
// 任何错误（未初始化、网络失败、反序列化失败）都当作 miss 返回，
// 绝不向上抛——这就是"缓存降级"的落地点：缓存坏了不影响业务正确性，只影响速度。
func Get(ctx context.Context, key string, dest any) bool {
	if rdb == nil {
		return false
	}
	data, err := rdb.Get(ctx, key).Bytes()
	if err != nil {
		if !errors.Is(err, redis.Nil) { // redis.Nil 是"key 不存在"的正常 miss，不算故障
			noteErr("get", err)
		}
		return false
	}
	if err := json.Unmarshal(data, dest); err != nil {
		noteErr("unmarshal", err)
		return false
	}
	noteOK()
	return true
}

// Set 序列化写缓存。失败仅打日志——写缓存失败不影响本次请求（数据已落 MySQL）。
func Set(ctx context.Context, key string, val any, ttl time.Duration) {
	if rdb == nil {
		return
	}
	data, err := json.Marshal(val)
	if err != nil {
		return
	}
	if err := rdb.Set(ctx, key, data, ttl).Err(); err != nil {
		noteErr("set", err)
	}
}

// Del 删除缓存（Cache-Aside 的"写后失效"）。调用方在数据变更成功后调用。
func Del(ctx context.Context, keys ...string) {
	if rdb == nil {
		return
	}
	if err := rdb.Del(ctx, keys...).Err(); err != nil {
		noteErr("del", err)
	}
}

// Incr 原子自增，供限流中间件使用。
// 与 Get/Set 不同：错误要返回给调用方，让调用方决定降级策略。
func Incr(ctx context.Context, key string) (int64, error) {
	if rdb == nil {
		return 0, errors.New("cache: redis not initialized")
	}
	return rdb.Incr(ctx, key).Result()
}

// Expire 设置 key 的过期时间（限流窗口用）。错误同样上抛。
func Expire(ctx context.Context, key string, ttl time.Duration) error {
	if rdb == nil {
		return errors.New("cache: redis not initialized")
	}
	return rdb.Expire(ctx, key, ttl).Err()
}

func noteErr(op string, err error) {
	if degraded.CompareAndSwap(false, true) {
		log.Printf("[cache] Redis 不可用(首次 %s 失败)，已降级为直查 MySQL: %v", op, err)
	}
}

func noteOK() {
	if degraded.CompareAndSwap(true, false) {
		log.Printf("[cache] Redis 已恢复，缓存重新生效")
	}
}
