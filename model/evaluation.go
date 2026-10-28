package model

import "time"

type Dataset struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

type DatasetItem struct {
	ID        string    `json:"id"`
	DatasetID string    `json:"dataset_id"`
	Input     string    `json:"input"`
	Reference string    `json:"reference"`
	CreatedAt time.Time `json:"created_at"`
}

type Evaluation struct {
	ID              string    `json:"id"`
	DatasetID       string    `json:"dataset_id"`
	PromptVersionID string    `json:"prompt_version_id"`
	Model           string    `json:"model"`
	JudgeType       string    `json:"judge_type"`
	Status          int       `json:"status"`
	AvgScore        float64   `json:"avg_score"`
	CreatedAt       time.Time `json:"created_at"`
}

type CompareResult struct {
	PromptVersionID string  `json:"prompt_version_id"`
	Ver             int     `json:"ver"`
	AvgScore        float64 `json:"avg_score"`
	Error           string  `json:"error,omitempty"`
}

type EvaluationResult struct {
	ID            string    `json:"id"`
	EvaluationID  string    `json:"evaluation_id"`
	DatasetItemID string    `json:"dataset_item_id"`
	Output        string    `json:"output"`
	Score         float64   `json:"score"`
	JudgeFeedback string    `json:"judge_feedback"`
	LatencyMs     int64     `json:"latency_ms"`
	TotalTokens   int64     `json:"total_tokens"`
	CreatedAt     time.Time `json:"created_at"`
}
