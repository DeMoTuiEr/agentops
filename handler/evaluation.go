package handler

import (
	"database/sql"
	"errors"
	"go-agentops/service"
	"go-agentops/store"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func CreateDataset(c *gin.Context) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ds, err := store.CreateDataset(req.Name, req.Description)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, ds)
}

func GetDatasetList(c *gin.Context) {
	list, err := store.GetDatasetList()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, list)
}

func GetDatasetByID(c *gin.Context) {
	id := c.Param("id")
	ds, items, err := store.GetDatasetByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"dataset": ds, "items": items})
}

func CreateDatasetItem(c *gin.Context) {
	datasetID := c.Param("id")
	var req struct {
		Input     string `json:"input"`
		Reference string `json:"reference"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	it, err := store.CreateDatasetItem(datasetID, req.Input, req.Reference)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "dataset not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusCreated, it)
}

func RunEvaluation(c *gin.Context) {
	var req struct {
		DatasetID       string `json:"dataset_id"`
		PromptVersionID string `json:"prompt_version_id"`
		Model           string `json:"model"`
		JudgeType       string `json:"judge_type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.JudgeType == "" {
		req.JudgeType = "exact_match"
	}
	eval, err := service.RunEvaluation(req.DatasetID, req.PromptVersionID, req.Model, req.JudgeType)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "dataset or prompt version not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusCreated, eval)
}

func GetEvaluationByID(c *gin.Context) {
	id := c.Param("id")
	ev, results, err := store.GetEvaluationByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"evaluation": ev, "results": results})
}

func GetEvaluationList(c *gin.Context) {
	datasetID := c.Query("dataset_id")
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid page"})
		return
	}
	pageSize, err := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid page_size"})
		return
	}
	// 夹紧分页参数：MySQL 预处理对负数 LIMIT/OFFSET 报 errno 1210（见 handler/trace.go 注释）
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	list, err := store.GetEvaluationList(datasetID, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, list)
}

func DeleteDataset(c *gin.Context) {
	id := c.Param("id")
	if err := store.DeleteDataset(id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "dataset not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

func DeleteDatasetItem(c *gin.Context) {
	datasetID := c.Param("id")
	itemID := c.Param("item_id")
	if err := store.DeleteDatasetItem(datasetID, itemID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "item not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

func CompareRun(c *gin.Context) {
	var req struct {
		DatasetID        string   `json:"dataset_id"`
		PromptVersionIDs []string `json:"prompt_version_ids"`
		Model            string   `json:"model"`
		JudgeType        string   `json:"judge_type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	res := service.CompareRun(req.DatasetID, req.PromptVersionIDs, req.Model, req.JudgeType)
	c.JSON(http.StatusOK, gin.H{"result": res})
}
