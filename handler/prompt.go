package handler

import (
	"database/sql"
	"errors"
	"go-agentops/store"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func GetPromptList(c *gin.Context) {
	prompt_list, err := store.GetPromptList()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, prompt_list)
}

func CreatePrompt(c *gin.Context) {
	var req struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	prompt, err := store.CreatePrompt(req.Name, req.Content)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, prompt)
}

func GetPromptByID(c *gin.Context) {
	id := c.Param("id")
	prompt, prompt_vers, err := store.GetPromptByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"prompt": prompt, "versions": prompt_vers})
}

func CreateVersionByID(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Content string `json:"content"`
	}
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	version, err := store.CreateVersionByID(id, req.Content)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, version)
}

func UpdateVersion(c *gin.Context) {
	id := c.Param("id")
	ver_str := c.Param("ver")
	ver, err := strconv.Atoi(ver_str)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	err = store.UpdateVersion(id, ver)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{})
}
