package service

import (
	"github.com/sashabaranov/go-openai"
)

var client *openai.Client

// InitLLM 初始化 DeepSeek 客户端。
// key 由 main 从 config 传入（收敛到统一配置入口，不再散落 os.Getenv）。
func InitLLM(key string) {
	var url = "https://api.deepseek.com"
	var cfg = openai.DefaultConfig(key)
	cfg.BaseURL = url
	client = openai.NewClientWithConfig(cfg)
}
