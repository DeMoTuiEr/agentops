package store

import (
	"context"
	"database/sql"
	"time"

	"go-agentops/cache"
	"go-agentops/model"

	"github.com/google/uuid"
)

func PromptBuild() error {
	_, err := db.Exec(
		`CREATE TABLE IF NOT EXISTS prompts(
    id         VARCHAR(36)  NOT NULL COMMENT 'UUID 主键',
    name       VARCHAR(255) NOT NULL COMMENT 'prompt 名称',
    cur_ver    INT          NOT NULL COMMENT '当前生效版本号',
    created_at DATETIME(3)  NOT NULL COMMENT '创建时间(UTC)',
    PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='prompt 主表(一个 prompt 多个版本)'`)
	if err != nil {
		return err
	}

	_, err = db.Exec(
		`CREATE TABLE IF NOT EXISTS prompt_versions(
    id         VARCHAR(36)  NOT NULL COMMENT 'UUID 主键',
    prompt_id  VARCHAR(36)  NOT NULL COMMENT '所属 prompt',
    ver        INT          NOT NULL COMMENT '版本号',
    content    MEDIUMTEXT   NOT NULL COMMENT 'prompt 内容(可含 {{input}} 占位符)',
    score      INT          NOT NULL DEFAULT 0 COMMENT '评测得分(-1 表示未评测)',
    created_at DATETIME(3)  NOT NULL,
    PRIMARY KEY (id),
    -- 唯一键一举三得：
    --   ① 并发建版本的最后防线——两个请求同时算出同一版本号时，后者撞唯一键报 1062，
    --      而不是静默写入脏数据；
    --   ② 最左前缀 prompt_id 天然充当 prompt_id 的查询索引，
    --      替代原来的 idx_prompt_versions_prompt_id，避免冗余索引(冗余索引浪费写性能)；
    --   ③ "同一 prompt 的版本号唯一"本来就是业务规则，应该建模成数据库约束而不是靠代码自觉。
    UNIQUE KEY uk_prompt_versions_prompt_ver (prompt_id, ver),
    CONSTRAINT fk_prompt_versions_prompt FOREIGN KEY (prompt_id) REFERENCES prompts(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='prompt 版本表'`)
	return err
}

// promptCacheEntry 是 GetPromptByID 的缓存值：
// 接口返回"prompt 主信息 + 全部版本列表"，一次缓存整个响应。
type promptCacheEntry struct {
	Prompt   model.Prompt          `json:"prompt"`
	Versions []model.PromptVersion `json:"versions"`
}

func cacheKeyPrompt(id string) string { return "agentops:prompt:" + id }

func GetPromptList() ([]model.Prompt, error) {
	var res = make([]model.Prompt, 0)
	rows, err := db.Query(`SELECT id,name,cur_ver,created_at FROM prompts ORDER BY created_at DESC`)
	if err != nil {
		return res, err
	}
	defer rows.Close()
	for rows.Next() {
		var temp model.Prompt
		err = rows.Scan(&temp.ID, &temp.Name, &temp.CurVer, &temp.CreatedAt)
		if err != nil {
			return res, err
		}
		res = append(res, temp)
	}
	err = rows.Err()
	return res, err
}

func CreatePrompt(name string, content string) (*model.Prompt, error) {
	// 真实事务：prompts 主表 + 第一个版本，两条 INSERT 要么都成功要么都不成功
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() // 惯用法：已 Commit 后再 Rollback 会返回 ErrTxDone，可安全忽略
	prompt_id := uuid.New().String()
	ver_id := uuid.New().String()
	t := time.Now().UTC()
	_, err = tx.Exec(`INSERT INTO prompts (id,name,cur_ver,created_at)VALUES (?,?,?,?)`, prompt_id, name, 1, t)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(`INSERT INTO prompt_versions(id,prompt_id,ver,score,content,created_at)VALUES (?,?,?,?,?,?)`, ver_id, prompt_id, 1, -1, content, t)
	if err != nil {
		return nil, err
	}
	err = tx.Commit()
	if err != nil {
		return nil, err
	}
	return &model.Prompt{
		ID:        prompt_id,
		Name:      name,
		CurVer:    1,
		CreatedAt: t,
	}, nil
}

// GetPromptByID Cache-Aside 三段式（同 GetTraceByID）。
// 与 trace 的区别：prompt 会被修改（建新版本/切换版本），所以写路径要主动删缓存，
// 见 CreateVersionByID / UpdateVersion 末尾的 cache.Del。
func GetPromptByID(id string) (*model.Prompt, []model.PromptVersion, error) {
	ctx := context.Background()

	var entry promptCacheEntry
	if cache.Get(ctx, cacheKeyPrompt(id), &entry) {
		return &entry.Prompt, entry.Versions, nil
	}

	var res1 model.Prompt
	row := db.QueryRow(`SELECT id,name,cur_ver,created_at FROM prompts WHERE id = ?`, id)
	if err := row.Scan(&res1.ID, &res1.Name, &res1.CurVer, &res1.CreatedAt); err != nil {
		return nil, nil, err
	}
	var res2 = make([]model.PromptVersion, 0)
	rows, err := db.Query(`SELECT id,prompt_id,ver,score,content,created_at FROM prompt_versions WHERE prompt_id = ? ORDER BY created_at DESC`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var temp model.PromptVersion
		err = rows.Scan(&temp.ID, &temp.PromptID, &temp.Ver, &temp.Score, &temp.Content, &temp.CreatedAt)
		if err != nil {
			return nil, nil, err
		}
		res2 = append(res2, temp)
	}
	if err = rows.Err(); err != nil {
		return nil, nil, err
	}

	cache.Set(ctx, cacheKeyPrompt(id), promptCacheEntry{Prompt: res1, Versions: res2}, cachePromptTTL)
	return &res1, res2, nil
}

func GetPromptVersionByID(id string) (model.PromptVersion, error) {
	var res model.PromptVersion
	row := db.QueryRow(`SELECT id,prompt_id,ver,score,content,created_at FROM prompt_versions WHERE id = ?`, id)
	err := row.Scan(&res.ID, &res.PromptID, &res.Ver, &res.Score, &res.Content, &res.CreatedAt)
	return res, err
}

// CreateVersionByID 创建新版本。
//
// 修掉的并发隐患（旧实现）：SELECT COUNT(*)+1 算版本号、然后单独 INSERT，
// 两个并发请求会算出同一个版本号。新版三层防护：
//  1. 事务：COUNT/MAX 计算和 INSERT 在同一事务里，要么都成要么都不成；
//  2. SELECT ... FOR UPDATE：对 prompts 行加排他锁(record lock，走主键，不锁表)，
//     并发"给同一 prompt 建版本"的请求在这里排队串行化，锁持有到事务提交。
//     注意它做的是"当前读"——读最新已提交数据；而 InnoDB 默认隔离级别
//     REPEATABLE READ 下普通 SELECT 读的是事务开始时的快照，可能读到旧版本号；
//  3. 唯一键 uk_prompt_versions_prompt_ver：前两层万一失效(未来改代码)，撞键报错兜底。
//
// 版本号用 MAX(ver)+1 而不是 COUNT(*)+1：如果历史版本被删过，
// COUNT+1 会算出已经存在的版本号，撞唯一键。
func CreateVersionByID(id string, content string) (model.PromptVersion, error) {
	var res model.PromptVersion
	ctx := context.Background()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback()

	// 先锁住 prompts 行：既校验存在性，又串行化并发建版本
	var curVer int
	err = tx.QueryRowContext(ctx, `SELECT cur_ver FROM prompts WHERE id = ? FOR UPDATE`, id).Scan(&curVer)
	if err != nil {
		return res, err // prompt 不存在 → sql.ErrNoRows，handler 映射成 404
	}

	var maxVer int
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(ver), 0) FROM prompt_versions WHERE prompt_id = ?`, id).Scan(&maxVer)
	if err != nil {
		return res, err
	}

	res = model.PromptVersion{
		ID:        uuid.New().String(),
		PromptID:  id,
		Ver:       maxVer + 1,
		Score:     -1,
		Content:   content,
		CreatedAt: time.Now().UTC(),
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO prompt_versions (id,prompt_id,ver,score,content,created_at)VALUES (?,?,?,?,?,?)`, res.ID, res.PromptID, res.Ver, res.Score, res.Content, res.CreatedAt)
	if err != nil {
		return res, err
	}

	if err = tx.Commit(); err != nil {
		return res, err
	}

	// Cache-Aside 写策略：先改 DB(已 Commit)，再删缓存。
	// 为什么是"删"而不是"更新"：并发写时两个写者可能以相反顺序更新缓存，
	// 留下永久脏数据；删除是幂等的，下次读自然回源加载最新值。
	// 注意顺序不能反：如果先删缓存再写 DB，中间窗口的读会回源到旧数据并回填，
	// 写完成后缓存里还是旧的。
	cache.Del(ctx, cacheKeyPrompt(id))
	return res, nil
}

func UpdateVersion(id string, ver int) error {
	// 校验该 prompt 下版本号存在（原先 ?AND 缺空格，SQLite 能容忍、MySQL 会报语法错误）
	var cnt int
	err := db.QueryRow(`SELECT COUNT(*) FROM prompt_versions WHERE prompt_id = ? AND ver = ?`, id, ver).Scan(&cnt)
	if err != nil {
		return err
	}
	if cnt == 0 {
		return sql.ErrNoRows
	}
	res, err := db.Exec(`UPDATE prompts SET cur_ver = ? WHERE id = ?`, ver, id)
	if err != nil {
		return err
	}
	// clientFoundRows=true 使 RowsAffected 返回"匹配行数"而非"改变行数"：
	// 重复 PUT 同一个 ver（cur_ver 已经是目标值）时 MySQL 默认返回 0 会误判 404，
	// 设了该参数后返回 1，语义与 SQLite 一致。
	rowcnt, _ := res.RowsAffected()
	if rowcnt == 0 {
		return sql.ErrNoRows
	}

	cache.Del(context.Background(), cacheKeyPrompt(id))
	return nil
}
