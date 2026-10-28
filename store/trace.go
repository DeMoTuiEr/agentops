package store

import (
	"context"

	"go-agentops/cache"
	"go-agentops/model"
)

// TraceBuild 建 traces 表。
// MySQL DDL 与 SQLite 时代的差异：
//   - TEXT 主键 → VARCHAR(36)（UUID 定长；MySQL 不允许 TEXT 列做主键/外键）
//   - 长文本 → MEDIUMTEXT（最多 16MB；LLM 回复可能超过 TEXT 的 64KB 上限）
//   - created_at TEXT → DATETIME(3)（毫秒精度，配合 parseTime 自动映射 time.Time；
//     同一秒内多条记录时毫秒才能保证 ORDER BY 排序稳定）
//   - 索引全部内联声明（MySQL 没有 CREATE INDEX IF NOT EXISTS 语法）
func TraceBuild() error {
	_, err := db.Exec(
		`CREATE TABLE IF NOT EXISTS traces(
    id           VARCHAR(36) NOT NULL COMMENT 'UUID 主键',
    session_id   VARCHAR(64) NOT NULL COMMENT '会话 ID',
    uid          VARCHAR(64) NOT NULL COMMENT '用户 ID',
    model        VARCHAR(64) NOT NULL COMMENT '模型名',
    prompt       MEDIUMTEXT  NOT NULL COMMENT '完整 prompt',
    response     MEDIUMTEXT  NOT NULL COMMENT '模型回复',
    total_tokens BIGINT      NOT NULL COMMENT '消耗 token 数',
    latency_ms   BIGINT      NOT NULL COMMENT '调用耗时(毫秒)',
    status       INT         NOT NULL DEFAULT 0 COMMENT '0=正常 1=异常',
    created_at   DATETIME(3) NOT NULL COMMENT '创建时间(UTC)',
    PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='LLM 调用轨迹(只增不改，缓存友好)'`)
	return err
}

// cacheKeyTrace 统一 key 规范：{业务前缀}:{实体}:{id}
// 前缀作用：与同实例其他业务数据隔离、出问题时方便用 SCAN 排查。
func cacheKeyTrace(id string) string { return "agentops:trace:" + id }

func CreateTrace(t *model.Trace) error {
	_, err := db.Exec(`INSERT INTO traces (id,session_id,uid,model,prompt,response,total_tokens,latency_ms,status,created_at)
VALUES (?,?,?,?,?,?,?,?,?,?)`, t.ID, t.SessionID, t.UID, t.Model, t.Prompt, t.Response, t.TotalTokens, t.LatencyMs, t.Status, t.CreatedAt)
	return err
}

// GetTraceByID 标准 Cache-Aside 三段式：
//  1. 先查 Redis，命中直接返回（不碰 MySQL）；
//  2. 未命中查 MySQL；
//  3. 回填缓存。
//
// trace 创建后不可变（没有任何 UPDATE 路径），所以只需要 TTL 过期，
// 不需要写后失效逻辑——这是"不可变数据"最理想的缓存场景。
//
// 不做的优化（面试被追问时的回答）：
//   - 缓存穿透（查不存在的 key 反复打库）：key 是 UUID，随机 ID 打 404 风险低，
//     标准解法(缓存空值/布隆过滤器)投入产出比不划算，不做；
//   - 缓存击穿（热点 key 过期瞬间并发打库）：单机学习项目量级到不了，标准解法
//     (互斥锁/singleflight)知道但判断当前不需要。
func GetTraceByID(id string) (*model.Trace, error) {
	ctx := context.Background()
	var res model.Trace

	if cache.Get(ctx, cacheKeyTrace(id), &res) {
		return &res, nil
	}

	row := db.QueryRow(`SELECT id,session_id,uid,model,prompt,response,total_tokens,latency_ms,status,created_at FROM traces WHERE id = ?`, id)
	if err := row.Scan(&res.ID, &res.SessionID, &res.UID, &res.Model, &res.Prompt, &res.Response, &res.TotalTokens, &res.LatencyMs, &res.Status, &res.CreatedAt); err != nil {
		return &res, err // ErrNoRows 不写缓存（不做空值缓存）
	}

	cache.Set(ctx, cacheKeyTrace(id), res, cacheTraceTTL)
	return &res, nil
}

func GetTraceList(uid string, ai_model string, page int, page_size int) ([]model.Trace, error) {
	var res = make([]model.Trace, 0)
	// ("" = ? OR uid = ?) 是"空串=不过滤"的开关技巧：SQLite 和 MySQL 都支持
	rows, err := db.Query(`SELECT id,session_id,uid,model,prompt,response,total_tokens,latency_ms,status,created_at FROM traces WHERE ("" = ? or uid = ?) and ("" = ? or model = ?)ORDER BY created_at DESC LIMIT ? OFFSET ?`, uid, uid, ai_model, ai_model, page_size, (page-1)*page_size)
	if err != nil {
		return res, err
	}
	defer rows.Close()
	for rows.Next() {
		var temp model.Trace
		err = rows.Scan(&temp.ID, &temp.SessionID, &temp.UID, &temp.Model, &temp.Prompt, &temp.Response, &temp.TotalTokens, &temp.LatencyMs, &temp.Status, &temp.CreatedAt)
		if err != nil {
			return res, err
		}
		res = append(res, temp)
	}
	err = rows.Err()
	return res, err
}
