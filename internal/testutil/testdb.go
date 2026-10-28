// Package testutil 提供测试基础设施。
package testutil

import (
	"database/sql"
	"fmt"
	"net"
	"os"
	"time"

	"go-agentops/config"

	"github.com/go-sql-driver/mysql"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// NewTestConfig 重建指定的测试库（DROP + CREATE）并返回指向它的 config.Config。
//
// 为什么 DROP 重建而不是 TRUNCATE：TRUNCATE 在有外键引用时会被拒绝(errno 1701)，
// 还得先关 FOREIGN_KEY_CHECKS 再删再开；直接重建整库最干净，还顺带验证建表 DDL。
//
// MySQL 不可达/无权限时返回 error，由调用方决定跳过（t.Skip / os.Exit(0)）还是失败——
// 集成测试不该因为环境缺失而红，纯单测(缓存/限流)不依赖 MySQL 仍然要跑。
func NewTestConfig(dbName string) (*config.Config, error) {
	host := envOr("TEST_DB_HOST", "127.0.0.1")
	port := envOr("TEST_DB_PORT", "3306")
	user := envOr("TEST_DB_USER", "agentops")
	password := envOr("TEST_DB_PASSWORD", "agentops123")

	// 管理连接：不带 DBName，用于执行 DROP/CREATE DATABASE
	adminCfg := mysql.Config{
		User:    user,
		Passwd:  password,
		Net:     "tcp",
		Addr:    net.JoinHostPort(host, port),
		Timeout: 3 * time.Second, // 短超时：MySQL 不在时快速失败，别让测试卡住
	}
	admin, err := sql.Open("mysql", adminCfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("测试库 DSN 配置错误: %w", err)
	}
	defer admin.Close()
	if err := admin.Ping(); err != nil {
		return nil, fmt.Errorf("MySQL 不可达: %w", err)
	}

	if _, err := admin.Exec("DROP DATABASE IF EXISTS " + dbName); err != nil {
		return nil, fmt.Errorf("删除旧测试库失败: %w", err)
	}
	if _, err := admin.Exec(fmt.Sprintf(
		"CREATE DATABASE %s CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", dbName)); err != nil {
		return nil, fmt.Errorf("创建测试库失败: %w", err)
	}

	return &config.Config{
		DBHost:     host,
		DBPort:     port,
		DBUser:     user,
		DBPassword: password,
		DBName:     dbName,
		// 测试里 TTL 短一点，方便必要时代码走 TTL 过期路径
		CacheTraceTTL:  time.Minute,
		CachePromptTTL: time.Minute,
	}, nil
}
