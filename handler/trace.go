package handler

import (
	"database/sql"
	"errors"
	"go-agentops/model"
	"go-agentops/store"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func CreateTrace(c *gin.Context) {
	var t model.Trace
	err := c.ShouldBindJSON(&t)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	t.ID = uuid.New().String()
	t.CreatedAt = time.Now().UTC()
	err = store.CreateTrace(&t)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, t)
}

func GetTrace(c *gin.Context) {
	id := c.Param("id")
	res, err := store.GetTraceByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, res)
}

func GetTraceList(c *gin.Context) {
	uid := c.Query("uid")
	ai_model := c.Query("model")
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid page"})
		return
	}
	page_size, err := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid page_size"})
		return
	}
	// 夹紧分页参数：MySQL 预处理语句对负数 LIMIT/OFFSET 直接报
	// "Incorrect arguments to mysqld_stmt_execute"(errno 1210) → 500；
	// SQLite 会把负数当 0/无限制忽略，这是两个数据库的行为差异。
	if page < 1 {
		page = 1
	}
	if page_size < 1 || page_size > 100 {
		page_size = 20
	}
	var trace_list []model.Trace
	trace_list, err = store.GetTraceList(uid, ai_model, page, page_size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, trace_list)
}
