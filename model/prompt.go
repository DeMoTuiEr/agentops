package model

import "time"

type PromptVersion struct {
	ID        string    `json:"id"`
	PromptID  string    `json:"prompt_id"`
	Ver       int       `json:"ver"`
	Score     int       `json:"score"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type Prompt struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CurVer    int       `json:"cur_ver"`
	CreatedAt time.Time `json:"created_at"`
}
