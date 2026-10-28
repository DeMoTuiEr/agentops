package store

import (
	"database/sql"
	"go-agentops/model"
	"time"

	"github.com/google/uuid"
)

func EvaluationBuild() error {
	_, err := db.Exec(
		`CREATE TABLE IF NOT EXISTS datasets(
    id          VARCHAR(36)  NOT NULL COMMENT 'UUID 主键',
    name        VARCHAR(255) NOT NULL COMMENT '数据集名称',
    description TEXT         NOT NULL COMMENT '描述(MySQL 的 TEXT 列不能写 DEFAULT '''', Go 侧总是显式赋值)',
    created_at  DATETIME(3)  NOT NULL,
    PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='评测数据集'`)
	if err != nil {
		return err
	}

	_, err = db.Exec(
		`CREATE TABLE IF NOT EXISTS dataset_items(
    id         VARCHAR(36) NOT NULL COMMENT 'UUID 主键',
    dataset_id VARCHAR(36) NOT NULL COMMENT '所属数据集',
    input      TEXT        NOT NULL COMMENT '评测输入(会填入 prompt 模板的 {{input}})',
    reference  TEXT        NOT NULL COMMENT '参考答案(LLM-as-Judge 的评分依据)',
    created_at DATETIME(3) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_dataset_items_dataset_id (dataset_id),
    CONSTRAINT fk_dataset_items_dataset FOREIGN KEY (dataset_id) REFERENCES datasets(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='数据集条目'`)
	if err != nil {
		return err
	}

	_, err = db.Exec(
		`CREATE TABLE IF NOT EXISTS evaluations(
    id                VARCHAR(36) NOT NULL COMMENT 'UUID 主键',
    dataset_id        VARCHAR(36) NOT NULL,
    prompt_version_id VARCHAR(36) NOT NULL,
    model             VARCHAR(64) NOT NULL COMMENT '被评测的模型名',
    judge_type        VARCHAR(32) NOT NULL DEFAULT 'exact_match' COMMENT '评分角度',
    status            INT         NOT NULL DEFAULT 0 COMMENT '1=评测中 2=完成 3=失败',
    avg_score         DOUBLE      NOT NULL DEFAULT 0 COMMENT '平均分(REAL 在 MySQL 是别名，直接写 DOUBLE 语义明确)',
    created_at        DATETIME(3) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_evaluations_dataset_id (dataset_id),
    KEY idx_evaluations_prompt_version_id (prompt_version_id),
    CONSTRAINT fk_evaluations_dataset FOREIGN KEY (dataset_id) REFERENCES datasets(id) ON DELETE CASCADE,
    CONSTRAINT fk_evaluations_prompt_version FOREIGN KEY (prompt_version_id) REFERENCES prompt_versions(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='评测任务'`)
	if err != nil {
		return err
	}

	_, err = db.Exec(
		`CREATE TABLE IF NOT EXISTS evaluation_results(
    id              VARCHAR(36) NOT NULL COMMENT 'UUID 主键',
    evaluation_id   VARCHAR(36) NOT NULL,
    dataset_item_id VARCHAR(36) NOT NULL,
    output          MEDIUMTEXT  NOT NULL COMMENT 'LLM 对输入的实际输出',
    score           DOUBLE      NOT NULL DEFAULT 0 COMMENT 'LLM-as-Judge 打分(0~1)',
    judge_feedback  TEXT        NOT NULL COMMENT '评分理由',
    latency_ms      BIGINT      NOT NULL DEFAULT 0,
    total_tokens    BIGINT      NOT NULL DEFAULT 0,
    created_at      DATETIME(3) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_evaluation_results_eval_id (evaluation_id),
    KEY idx_evaluation_results_item_id (dataset_item_id),
    CONSTRAINT fk_evaluation_results_eval FOREIGN KEY (evaluation_id) REFERENCES evaluations(id) ON DELETE CASCADE,
    CONSTRAINT fk_evaluation_results_item FOREIGN KEY (dataset_item_id) REFERENCES dataset_items(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='评测结果明细'`)
	if err != nil {
		return err
	}

	return nil
}

func CreateDataset(name, description string) (*model.Dataset, error) {
	d := &model.Dataset{
		ID:          uuid.New().String(),
		Name:        name,
		Description: description,
		CreatedAt:   time.Now().UTC(),
	}
	_, err := db.Exec(`INSERT INTO datasets (id, name, description, created_at) VALUES (?,?,?,?)`,
		d.ID, d.Name, d.Description, d.CreatedAt)
	if err != nil {
		return nil, err
	}
	return d, nil
}

func GetDatasetList() ([]model.Dataset, error) {
	res := make([]model.Dataset, 0)
	rows, err := db.Query(`SELECT id, name, description, created_at FROM datasets ORDER BY created_at DESC`)
	if err != nil {
		return res, err
	}
	defer rows.Close()
	for rows.Next() {
		var d model.Dataset
		if err := rows.Scan(&d.ID, &d.Name, &d.Description, &d.CreatedAt); err != nil {
			return res, err
		}
		res = append(res, d)
	}
	return res, rows.Err()
}

func GetDatasetByID(id string) (*model.Dataset, []model.DatasetItem, error) {
	var ds model.Dataset
	row := db.QueryRow(`SELECT id, name, description, created_at FROM datasets WHERE id = ?`, id)
	if err := row.Scan(&ds.ID, &ds.Name, &ds.Description, &ds.CreatedAt); err != nil {
		return nil, nil, err
	}

	items := make([]model.DatasetItem, 0)
	rows, err := db.Query(`SELECT id, dataset_id, input, reference, created_at FROM dataset_items WHERE dataset_id = ? ORDER BY created_at ASC`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var it model.DatasetItem
		if err := rows.Scan(&it.ID, &it.DatasetID, &it.Input, &it.Reference, &it.CreatedAt); err != nil {
			return nil, nil, err
		}
		items = append(items, it)
	}
	return &ds, items, rows.Err()
}

func CreateDatasetItem(datasetID, input, reference string) (*model.DatasetItem, error) {
	// 校验 dataset 存在
	var cnt int
	if err := db.QueryRow(`SELECT COUNT(*) FROM datasets WHERE id = ?`, datasetID).Scan(&cnt); err != nil {
		return nil, err
	}
	if cnt == 0 {
		return nil, sql.ErrNoRows
	}

	it := &model.DatasetItem{
		ID:        uuid.New().String(),
		DatasetID: datasetID,
		Input:     input,
		Reference: reference,
		CreatedAt: time.Now().UTC(),
	}
	_, err := db.Exec(`INSERT INTO dataset_items (id, dataset_id, input, reference, created_at) VALUES (?,?,?,?,?)`,
		it.ID, it.DatasetID, it.Input, it.Reference, it.CreatedAt)
	if err != nil {
		return nil, err
	}
	return it, nil
}

func DeleteDatasetItem(datasetID, itemID string) error {
	res, err := db.Exec(`DELETE FROM dataset_items WHERE id = ? AND dataset_id = ?`, itemID, datasetID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func InsertEvaluation(eval model.Evaluation) error {
	_, err := db.Exec(
		`INSERT INTO evaluations (id, dataset_id, prompt_version_id, model, judge_type, status, avg_score, created_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		eval.ID, eval.DatasetID, eval.PromptVersionID, eval.Model, eval.JudgeType, eval.Status, eval.AvgScore, eval.CreatedAt)
	return err
}

func InsertEvaluationResult(result model.EvaluationResult) error {
	_, err := db.Exec(
		`INSERT INTO evaluation_results (id, evaluation_id, dataset_item_id, output, score, judge_feedback, latency_ms, total_tokens, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		result.ID, result.EvaluationID, result.DatasetItemID, result.Output, result.Score, result.JudgeFeedback, result.LatencyMs, result.TotalTokens, result.CreatedAt)
	return err
}

func UpdateEvaluationCompleted(id string, status int, avgScore float64) error {
	_, err := db.Exec(`UPDATE evaluations SET status = ?, avg_score = ? WHERE id = ?`, status, avgScore, id)
	return err
}

func GetEvaluationByID(id string) (*model.Evaluation, []model.EvaluationResult, error) {
	var ev model.Evaluation
	row := db.QueryRow(`SELECT id, dataset_id, prompt_version_id, model, judge_type, status, avg_score, created_at FROM evaluations WHERE id = ?`, id)
	if err := row.Scan(&ev.ID, &ev.DatasetID, &ev.PromptVersionID, &ev.Model, &ev.JudgeType, &ev.Status, &ev.AvgScore, &ev.CreatedAt); err != nil {
		return nil, nil, err
	}

	results := make([]model.EvaluationResult, 0)
	rows, err := db.Query(`SELECT id, evaluation_id, dataset_item_id, output, score, judge_feedback, latency_ms, total_tokens, created_at FROM evaluation_results WHERE evaluation_id = ? ORDER BY created_at ASC`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r model.EvaluationResult
		if err := rows.Scan(&r.ID, &r.EvaluationID, &r.DatasetItemID, &r.Output, &r.Score, &r.JudgeFeedback, &r.LatencyMs, &r.TotalTokens, &r.CreatedAt); err != nil {
			return nil, nil, err
		}
		results = append(results, r)
	}
	return &ev, results, rows.Err()
}

func GetEvaluationList(datasetID string, page, pageSize int) ([]model.Evaluation, error) {
	res := make([]model.Evaluation, 0)
	rows, err := db.Query(
		`SELECT id, dataset_id, prompt_version_id, model, judge_type, status, avg_score, created_at FROM evaluations WHERE ('' = ? OR dataset_id = ?) ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		datasetID, datasetID, pageSize, (page-1)*pageSize)
	if err != nil {
		return res, err
	}
	defer rows.Close()
	for rows.Next() {
		var ev model.Evaluation
		if err := rows.Scan(&ev.ID, &ev.DatasetID, &ev.PromptVersionID, &ev.Model, &ev.JudgeType, &ev.Status, &ev.AvgScore, &ev.CreatedAt); err != nil {
			return res, err
		}
		res = append(res, ev)
	}
	return res, rows.Err()
}

func DeleteDataset(id string) error {
	res, err := db.Exec(`DELETE FROM datasets WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
