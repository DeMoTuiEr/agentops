package model

import "time"

type Trace struct {
	ID          string    `json:"id"`
	SessionID   string    `json:"session_id"`
	UID         string    `json:"uid"`
	Model       string    `json:"model"`
	Prompt      string    `json:"prompt"`
	Response    string    `json:"response"`
	TotalTokens int64     `json:"total_tokens"`
	LatencyMs   int64     `json:"latency_ms"`
	Status      int       `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}
