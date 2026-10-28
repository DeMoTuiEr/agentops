package service

import (
	"context"
	"encoding/json"
	"fmt"
	"go-agentops/model"
	"go-agentops/store"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sashabaranov/go-openai"
)

// 注意：SQLite 时代的 dbMu sync.Mutex 已删除。
// 当时加锁是为了串行化 SQLite 的并发写（SQLite 单写者模型，多 goroutine 同时写会报
// database is locked）；MySQL 的 InnoDB 天然支持并发写（行级锁 + MVCC），
// 连接池会管理并发连接，业务代码不需要也不应该再加全局锁——加了反而把并发写退化成串行。

// AiUse  调用 LLM，返回 output、latency(ms)、tokens
func AiUse(prompt model.PromptVersion, input string) (string, int64, int64) {
	content := strings.Replace(prompt.Content, "{{input}}", input, -1)
	req := openai.ChatCompletionRequest{
		Model: "deepseek-chat",
		Messages: []openai.ChatCompletionMessage{
			{Role: "user", Content: content},
		},
	}
	ctx := context.Background()
	st := time.Now()
	resp, err := client.CreateChatCompletion(ctx, req)
	if err != nil {
		return err.Error(), 0, 0
	}
	latency := time.Since(st).Milliseconds()
	respstr := resp.Choices[0].Message.Content
	tokens := resp.Usage.TotalTokens
	return respstr, latency, int64(tokens)
}

// AiReview 评测：比较 output 和 reference，返回分数和评语
func AiReview(output, reference, judgeType string) (float64, string) {
	content := fmt.Sprintf("请你做出0~1分的评分并加上评语 回复格式是json 内容必须限定为{\"score\":XXX,\"feedback\":XXX} XXX是你可以修改的地方 可以是小数 需按照{%s}角度评分 如下 结果{%s} 预期{%s}", judgeType, output, reference)
	req := openai.ChatCompletionRequest{
		Model: "deepseek-chat",
		Messages: []openai.ChatCompletionMessage{
			{Role: "user", Content: content},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{Type: "json_object"},
	}
	ctx := context.Background()
	resp, err := client.CreateChatCompletion(ctx, req)
	if err != nil {
		return -1.0, err.Error()
	}
	respstr := resp.Choices[0].Message.Content
	var res struct {
		Score    float64 `json:"score"`
		Feedback string  `json:"feedback"`
	}
	err = json.Unmarshal([]byte(respstr), &res)
	if err != nil {
		return -1.0, err.Error()
	}
	return res.Score, res.Feedback
}

// LLMAnswer 执行单条：填充模板 → 调 LLM → 返回 EvaluationResult
func LLMAnswer(item model.DatasetItem, prompt model.PromptVersion, evaluationID string) (model.EvaluationResult, error) {
	var res model.EvaluationResult
	res.ID = uuid.New().String()
	res.EvaluationID = evaluationID
	res.DatasetItemID = item.ID
	res.CreatedAt = time.Now().UTC()
	res.Output, res.TotalTokens, res.LatencyMs = AiUse(prompt, item.Input)
	return res, nil
}

// RunEvaluation 执行一次完整的评测：
// 插入评测任务 → 取数据集条目和 prompt 版本 → 并发调用 LLM 生成 + LLM-as-Judge 打分 → 汇总。
//
// 并发模型（面试常问）：WaitGroup 等待所有 goroutine，带缓冲 channel 收集错误。
// 之前 scoreSum 是多个 goroutine 无保护累加同一变量（数据竞争），当时恰好被 dbMu 串行化掩盖；
// 现在改为每个 goroutine 只写自己下标的 scores[i]，wg.Wait() 之后在主 goroutine 求和——
// "不同下标互不干扰"是最简单的无锁并发写法。
func RunEvaluation(datasetID, promptVersionID, aiModel, judgeType string) (model.Evaluation, error) {
	eval := model.Evaluation{
		ID:              uuid.New().String(),
		DatasetID:       datasetID,
		PromptVersionID: promptVersionID,
		Model:           aiModel,
		JudgeType:       judgeType,
		Status:          1, // running
		AvgScore:        -1,
		CreatedAt:       time.Now().UTC(),
	}
	if err := store.InsertEvaluation(eval); err != nil {
		return eval, err
	}

	_, datasetItems, err := store.GetDatasetByID(datasetID)
	if err != nil {
		return eval, err
	}

	promptVersion, err := store.GetPromptVersionByID(promptVersionID)
	if err != nil {
		return eval, err
	}

	if len(datasetItems) == 0 {
		eval.Status = 2
		eval.AvgScore = 0
		if err := store.UpdateEvaluationCompleted(eval.ID, 2, 0); err != nil {
			return eval, err
		}
		return eval, nil
	}

	scores := make([]float64, len(datasetItems))
	var wg sync.WaitGroup
	errCh := make(chan error, len(datasetItems))
	for i, item := range datasetItems {
		wg.Add(1)
		go func(i int, item model.DatasetItem) {
			defer wg.Done()
			resTemp, err := LLMAnswer(item, promptVersion, eval.ID)
			if err != nil {
				errCh <- err
				return
			}
			resTemp.Score, resTemp.JudgeFeedback = AiReview(resTemp.Output, item.Reference, judgeType)
			// 只写自己的下标：不同 goroutine 写不同内存位置，无数据竞争，
			// 因此也不再需要全局锁（MySQL 连接池天然支持并发写）
			scores[i] = resTemp.Score
			if err := store.InsertEvaluationResult(resTemp); err != nil {
				errCh <- err
				return
			}
		}(i, item)
	}
	wg.Wait()
	close(errCh)
	for err = range errCh {
		_ = store.UpdateEvaluationCompleted(eval.ID, 3, -1)
		return eval, err
	}
	var scoreSum float64
	for _, s := range scores {
		scoreSum += s
	}
	eval.AvgScore = scoreSum / float64(len(datasetItems))
	eval.Status = 2
	if err = store.UpdateEvaluationCompleted(eval.ID, 2, eval.AvgScore); err != nil {
		return eval, err
	}

	return eval, nil
}

func CompareRun(datasetID string, promptVersionIDs []string, aiModel, judgeType string) []model.CompareResult {
	var wgsum sync.WaitGroup
	res := make(chan model.CompareResult, len(promptVersionIDs))
	for _, promptVersionID := range promptVersionIDs {
		wgsum.Add(1)
		go func(promptVersionID string) {
			defer wgsum.Done()
			pv, err := store.GetPromptVersionByID(promptVersionID)
			if err != nil {
				res <- model.CompareResult{PromptVersionID: promptVersionID, Error: err.Error()}
				return
			}
			temp, err := RunEvaluation(datasetID, promptVersionID, aiModel, judgeType)
			if err != nil {
				res <- model.CompareResult{
					PromptVersionID: promptVersionID,
					Ver:             pv.Ver,
					AvgScore:        -1,
					Error:           err.Error(),
				}
				return
			}
			res <- model.CompareResult{
				PromptVersionID: promptVersionID,
				Ver:             pv.Ver,
				AvgScore:        temp.AvgScore,
				Error:           "",
			}
		}(promptVersionID)

	}
	wgsum.Wait()
	resslice := make([]model.CompareResult, 0)
	close(res)
	for temp := range res {
		resslice = append(resslice, temp)
	}
	return resslice
}
