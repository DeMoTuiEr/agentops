package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go-agentops/cache"
	"go-agentops/config"
	"go-agentops/handler"
	"go-agentops/middleware"
	"go-agentops/service"
	"go-agentops/store"

	"github.com/gin-gonic/gin"
)

func main() {
	cfg := config.Load()

	// 启动顺序有讲究：
	//   MySQL 是"生命线"——存数据的，初始化失败直接退出（不退出就是裸奔）；
	//   Redis 是"加速器"——缓存和限流，初始化失败只告警，降级运行。
	if err := store.Init(cfg); err != nil {
		log.Fatalf("MySQL 初始化失败: %v", err)
	}
	defer store.Close()

	if err := cache.Init(cfg); err != nil {
		log.Printf("[warn] Redis 初始化失败，缓存与限流降级运行: %v", err)
	}
	defer cache.Close()

	service.InitLLM(cfg.DeepSeekAPIKey)

	router := gin.Default() // gin.Default 自带 Logger + Recovery 中间件
	// 本地部署没有反向代理：不信任任何代理头，ClientIP 直接用 TCP 对端地址，
	// 防止伪造 X-Forwarded-For 绕过限流
	_ = router.SetTrustedProxies(nil)

	// 探活接口放在限流组外：运维探活不该被限流计数
	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"mysql": "ok", "redis": cache.Enabled()})
	})

	api := router.Group("/api/v1")
	api.Use(middleware.RateLimit(cfg.RateLimitCount, cfg.RateLimitWindow))
	{
		api.POST("/traces", handler.CreateTrace)
		api.GET("/traces/:id", handler.GetTrace)
		api.GET("/traces", handler.GetTraceList)
		api.GET("/prompts", handler.GetPromptList)
		api.POST("/prompts", handler.CreatePrompt)
		api.GET("/prompts/:id", handler.GetPromptByID)
		api.POST("/prompts/:id/versions", handler.CreateVersionByID)
		api.PUT("/prompts/:id/versions/:ver", handler.UpdateVersion)
		api.POST("/datasets", handler.CreateDataset)
		api.GET("/datasets", handler.GetDatasetList)
		api.GET("/datasets/:id", handler.GetDatasetByID)
		api.POST("/datasets/:id/items", handler.CreateDatasetItem)
		api.DELETE("/datasets/:id/items/:item_id", handler.DeleteDatasetItem)
		api.DELETE("/datasets/:id", handler.DeleteDataset)
		api.GET("/evaluations", handler.GetEvaluationList)
		api.POST("/evaluations", handler.RunEvaluation)
		api.GET("/evaluations/:id", handler.GetEvaluationByID)
		api.POST("/evaluations/compare", handler.CompareRun)
	}

	// 优雅关闭（面试常问"线上怎么不停机更新"的答案）：
	// 1. signal.NotifyContext 把 Ctrl+C / SIGTERM 转成一个 context 取消信号；
	// 2. 收到信号后 srv.Shutdown：停止接收新连接，等正在处理的请求跑完（最多等 5 秒）；
	// 3. 关 HTTP 之后再关 Redis/MySQL 连接池（先关入口再关依赖，顺序不能反）。
	srv := &http.Server{Addr: ":" + cfg.ServerPort, Handler: router}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP 服务异常退出: %v", err)
		}
	}()
	log.Printf("服务已启动 :%s", cfg.ServerPort)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	log.Println("收到退出信号，开始优雅关闭...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP 关闭超时: %v", err)
	}
	_ = cache.Close()
	_ = store.Close()
	log.Println("已退出")
}
