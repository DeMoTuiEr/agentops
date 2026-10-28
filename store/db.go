package store

import (
	"database/sql"
	"net"
	"time"

	"go-agentops/config"

	"github.com/go-sql-driver/mysql"
)

// db 是全局唯一的连接池句柄（database/sql 的 *sql.DB 本身就是连接池，
// 不是单个连接——这是和 SQLite 时代"打开一个文件"最大的思维差异）。
var db *sql.DB

// 缓存 TTL：Init 时从 config 拷到包级变量，避免每个函数都传 cfg。
var cacheTraceTTL time.Duration
var cachePromptTTL time.Duration

// Init 打开 MySQL 连接池并建表。与 SQLite 时代的区别：
//  1. 用 mysql.Config + FormatDSN() 而不是手拼字符串——密码里含 @ : / 等特殊字符时手拼必炸；
//  2. InnoDB 天然支持并发写、外键始终生效、崩溃自动恢复，原来那 3 条 PRAGMA 全部删除；
//  3. 显式配置连接池参数（SQLite 是单文件，没有"连接"概念，所以当时没配）。
func Init(cfg *config.Config) error {
	cacheTraceTTL = cfg.CacheTraceTTL
	cachePromptTTL = cfg.CachePromptTTL

	var err error
	// sql.Open 只校验 DSN 格式并注册连接池，不会真的建连接（惰性连接）。
	// 真正探活靠下面的 Ping。
	db, err = sql.Open("mysql", buildDSN(cfg))
	if err != nil {
		return err
	}

	// 连接池参数（面试常问，每个都有明确理由）：
	//   MaxOpenConns：并发连接上限。MySQL 默认 max_connections=151，
	//                 压测时 goroutine 再多也只会用 20 条连接，防止打爆服务端；
	//   MaxIdleConns：空闲连接保留数，太少则请求高峰后连接被反复创建销毁；
	//   ConnMaxLifetime：单条连接最长存活时间。必须小于 MySQL 的 wait_timeout(默认 8h)，
	//                     否则连接池会拿到一条被服务端悄悄关闭的"死连接"。
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(30 * time.Minute)

	if err = db.Ping(); err != nil { // 真正建立第一条连接，MySQL 不可达在这里暴露
		return err
	}

	if err = TraceBuild(); err != nil {
		return err
	}
	if err = PromptBuild(); err != nil {
		return err
	}
	return EvaluationBuild()
}

// buildDSN 用驱动提供的结构化配置拼 DSN。
// 与手拼字符串相比：字段值会自动做 URL 转义，且参数名不会拼错。
func buildDSN(cfg *config.Config) string {
	c := mysql.Config{
		User:   cfg.DBUser,
		Passwd: cfg.DBPassword,
		Net:    "tcp",
		Addr:   net.JoinHostPort(cfg.DBHost, cfg.DBPort),
		DBName: cfg.DBName,

		// 时间三件套：DATETIME<->time.Time 互转，且统一切到 UTC。
		// ParseTime=true：DATETIME 列 Scan 进 time.Time（否则只能进 []byte/string）；
		// Loc=time.UTC：写入/读回都按 UTC 解释。绝不写 Loc=Local——
		//   DATETIME 本身不存时区，本机时区一变，数据含义就漂移。
		ParseTime: true,
		Loc:       time.UTC,

		// 关键参数：让 UPDATE 的 RowsAffected 返回"匹配行数"而非"实际改变行数"。
		// MySQL 默认 changed rows：UPDATE 把值改成原值时返回 0；
		// 而 SQLite 返回 matched rows。不设这个参数，重复 PUT 同一版本号会被误判 404。
		ClientFoundRows: true,

		Timeout:      5 * time.Second,  // 建立 TCP 连接的超时
		ReadTimeout:  10 * time.Second, // 单次查询读超时（防慢查询把请求挂死）
		WriteTimeout: 10 * time.Second, // 单次查询写超时

		Params: map[string]string{
			// utf8mb4 才是完整 UTF-8（支持 emoji）；MySQL 的 "utf8" 是 3 字节残废版，别用
			"charset": "utf8mb4",
		},
	}
	return c.FormatDSN()
}

func Close() error { return db.Close() }
