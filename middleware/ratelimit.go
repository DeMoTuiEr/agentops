// Package middleware 存放 gin 中间件。
package middleware

import (
	"net/http"
	"strconv"
	"time"

	"go-agentops/cache"

	"github.com/gin-gonic/gin"
)

// RateLimit 基于 Redis 的固定窗口限流中间件。
//
// 原理（面试白板题）：以客户端 IP 为维度，窗口内第一个请求 INCR 出 1，
// 这时给 key 设置窗口期 TTL；后续请求 INCR 出的值就是"窗口内第几个请求"，
// 超过阈值直接 429。窗口过期后 key 自动消失，计数归零。
//
// 两个已知缺陷（要能主动讲出来，而不是被问倒）：
//  1. 固定窗口在边界处有"双倍突发"：第 1 分钟最后一毫秒打满 N 次 +
//     第 2 分钟第一毫秒又打满 N 次，瞬间 2N。标准改进是滑动窗口(ZSET 存时间戳)
//     或令牌桶；本项目量级用固定窗口足够，选它是因为实现最简、语义直观。
//  2. INCR 和 EXPIRE 是两个命令，中间进程崩溃会留下永不过期的 key。
//     生产环境的标准解法是把两件事写进一个 Lua 脚本用 EVAL 原子执行。
//
// 降级策略：Redis 不可用时 fail-open（放行）。
// 限流是"保护措施"，不能因为保护组件自己挂了就把所有正常请求拒掉——
// 可用性优先于限流目标。
func RateLimit(limit int, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context() // 用请求上下文：客户端断开时 Redis 调用可随之中止
		key := "agentops:ratelimit:" + c.ClientIP()

		cnt, err := cache.Incr(ctx, key)
		if err != nil {
			c.Next() // Redis 故障 → 放行（fail-open）
			return
		}
		if cnt == 1 {
			// 只在窗口的第一个请求上设置过期时间；
			// 如果每个请求都 EXPIRE，等于不断给窗口续期，窗口永远不会结束
			_ = cache.Expire(ctx, key, window)
		}

		remaining := int64(limit) - cnt
		if remaining < 0 {
			remaining = 0
		}
		c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
		c.Header("X-RateLimit-Remaining", strconv.FormatInt(remaining, 10))

		if cnt > int64(limit) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "请求过于频繁，请稍后重试",
			})
			return
		}
		c.Next()
	}
}
