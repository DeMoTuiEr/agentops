package config

import (
	"log"
	"os"
	"strconv"
	"time"
)

// Config 集中管理运行期配置，全部来自环境变量（没设置时用默认值）。
//
// 设计取舍：刻意不引 viper——环境变量 + 默认值已经覆盖本地开发 / Docker 部署 /
// CI 三种场景（这就是 12-factor 的"配置外部化"），少一个依赖、少一层间接，
// 面试被问"为什么不用配置中心/viper"时能讲清楚理由。
// 默认值全部对齐 docker-compose.yml 里的账号密码。
type Config struct {
	ServerPort string

	// MySQL 连接参数
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string

	// Redis
	RedisAddr     string
	RedisPassword string
	RedisDB       int

	// 缓存 TTL
	CacheTraceTTL  time.Duration // trace 创建后不可变，可以久一点
	CachePromptTTL time.Duration // prompt 会被改，靠写后删缓存保证一致性，TTL 只是兜底

	// 限流（固定窗口：每窗口内每个 IP 最多多少个请求）
	RateLimitCount  int
	RateLimitWindow time.Duration

	DeepSeekAPIKey string
}

// Load 读取环境变量并组装 Config。非法值（如 RATE_LIMIT_COUNT=abc）打日志后回落默认值，
// 而不是 panic——配置错误不该让整个进程起不来。
func Load() *Config {
	return &Config{
		ServerPort: getEnv("SERVER_PORT", "8080"),

		DBHost:     getEnv("DB_HOST", "127.0.0.1"),
		DBPort:     getEnv("DB_PORT", "3306"),
		DBUser:     getEnv("DB_USER", "agentops"),
		DBPassword: getEnv("DB_PASSWORD", "agentops123"),
		DBName:     getEnv("DB_NAME", "agentops"),

		RedisAddr:     getEnv("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		RedisDB:       getEnvInt("REDIS_DB", 0),

		CacheTraceTTL:  getEnvDuration("CACHE_TTL_TRACE", 10*time.Minute),
		CachePromptTTL: getEnvDuration("CACHE_TTL_PROMPT", 5*time.Minute),

		RateLimitCount:  getEnvInt("RATE_LIMIT_COUNT", 100),
		RateLimitWindow: getEnvDuration("RATE_LIMIT_WINDOW", time.Minute),

		DeepSeekAPIKey: getEnv("DEEPSEEK_API_KEY", ""),
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Printf("[config] %s=%q 不是整数，使用默认值 %d", key, v, def)
		return def
	}
	return n
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v) // 支持 "1m" "30s" "500ms" 这种写法
	if err != nil {
		log.Printf("[config] %s=%q 不是合法时长，使用默认值 %v", key, v, def)
		return def
	}
	return d
}
