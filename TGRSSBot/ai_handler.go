package main

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// AIHandler AI功能处理器
type AIHandler struct {
	service AIService
	cache   *AICache
	db      *sql.DB
}

// NewAIHandler 创建AI处理器
func NewAIHandler(service AIService, db *sql.DB) *AIHandler {
	return &AIHandler{
		service: service,
		cache:   NewAICache(db),
		db:      db,
	}
}

// HandleTranslateRequest 处理翻译请求
func (h *AIHandler) HandleTranslateRequest(ctx context.Context, text, sourceLang, targetLang string) (*TranslateResult, error) {
	// 生成内容哈希用于缓存
	contentHash := generateContentHash(text, "translate", sourceLang, targetLang)

	// 检查缓存
	if cachedResult, found := h.cache.GetCachedTranslation(contentHash); found {
		logMessage(
			"debug", "翻译缓存命中")
		return cachedResult, nil
	}

	// 调用AI服务进行翻译
	result, err := h.service.Translate(ctx, text, sourceLang, targetLang)
	if err != nil {
		return nil, err
	}

	// 缓存结果
	if err := h.cache.CacheTranslation(contentHash, result); err != nil {
		logMessage("warn", fmt.Sprintf("缓存翻译结果失败: %v", err))
	}

	// 记录使用统计
	h.recordUsage("translate", result.TokensUsed, calculateCost(result.TokensUsed, result.Provider))

	return result, nil
}

// HandleSummarizeRequest 处理摘要请求
func (h *AIHandler) HandleSummarizeRequest(ctx context.Context, text string, maxLength, minLength int) (*SummaryResult, error) {
	// 生成内容哈希用于缓存
	contentHash := generateContentHash(text, "summarize", fmt.Sprintf("%d-%d", maxLength, minLength))

	// 检查缓存
	if cachedResult, found := h.cache.GetCachedSummary(contentHash); found {
		logMessage("debug", "摘要缓存命中")
		return cachedResult, nil
	}

	// 调用AI服务进行摘要
	result, err := h.service.Summarize(ctx, text, maxLength, minLength)
	if err != nil {
		return nil, err
	}

	// 缓存结果
	if err := h.cache.CacheSummary(contentHash, result); err != nil {
		logMessage("warn", fmt.Sprintf("缓存摘要结果失败: %v", err))
	}

	// 记录使用统计
	h.recordUsage("summarize", result.TokensUsed, calculateCost(result.TokensUsed, result.Provider))

	return result, nil
}

// recordUsage 记录AI使用统计
func (h *AIHandler) recordUsage(operationType string, tokensUsed int, cost float64) {
	today := time.Now().Format("2006-01-02")

	err := withDB(func(db *sql.DB) error {
		// 检查今日记录是否存在
		var count int
		err := db.QueryRow("SELECT COUNT(*) FROM ai_usage_stats WHERE date = ?", today).Scan(&count)
		if err != nil {
			return err
		}

		if count > 0 {
			// 更新现有记录
			if operationType == "translate" {
				_, err = db.Exec(`
					UPDATE ai_usage_stats 
					SET translate_count = translate_count + 1, 
						total_tokens = total_tokens + ?, 
						total_cost = total_cost + ?,
						updated_at = CURRENT_TIMESTAMP
					WHERE date = ?`, tokensUsed, cost, today)
			} else {
				_, err = db.Exec(`
					UPDATE ai_usage_stats 
					SET summarize_count = summarize_count + 1, 
						total_tokens = total_tokens + ?, 
						total_cost = total_cost + ?,
						updated_at = CURRENT_TIMESTAMP
					WHERE date = ?`, tokensUsed, cost, today)
			}
		} else {
			// 插入新记录
			translateCount := 0
			summarizeCount := 0
			if operationType == "translate" {
				translateCount = 1
			} else {
				summarizeCount = 1
			}

			_, err = db.Exec(`
				INSERT INTO ai_usage_stats (date, translate_count, summarize_count, total_tokens, total_cost)
				VALUES (?, ?, ?, ?, ?)`, today, translateCount, summarizeCount, tokensUsed, cost)
		}
		return err
	})

	if err != nil {
		logMessage("error", fmt.Sprintf("记录AI使用统计失败: %v", err))
	}
}

// AICache AI结果缓存系统
type AICache struct {
	db *sql.DB
}

// NewAICache 创建AI缓存
func NewAICache(db *sql.DB) *AICache {
	return &AICache{db: db}
}

// GetCachedTranslation 获取缓存的翻译结果
func (c *AICache) GetCachedTranslation(contentHash string) (*TranslateResult, bool) {
	var result TranslateResult
	var originalContent, processedContent, sourceLang, targetLang, provider, model string
	var tokensUsed int
	var processingTime int64
	var createdAt time.Time

	err := withDB(func(db *sql.DB) error {
		return db.QueryRow(`
			SELECT original_content, processed_content, source_lang, target_lang, 
				   provider, model, tokens_used, processing_time, created_at
			FROM ai_processing_records 
			WHERE content_hash = ? AND content_type = 'translate'`, contentHash).Scan(
			&originalContent, &processedContent, &sourceLang, &targetLang,
			&provider, &model, &tokensUsed, &processingTime, &createdAt)
	})

	if err != nil {
		return nil, false
	}

	result = TranslateResult{
		OriginalText:   originalContent,
		TranslatedText: processedContent,
		SourceLang:     sourceLang,
		TargetLang:     targetLang,
		Provider:       provider,
		Model:          model,
		TokensUsed:     tokensUsed,
		ProcessingTime: processingTime,
		CreatedAt:      createdAt,
	}

	return &result, true
}

// GetCachedSummary 获取缓存的摘要结果
func (c *AICache) GetCachedSummary(contentHash string) (*SummaryResult, bool) {
	var result SummaryResult
	var originalContent, processedContent, provider, model string
	var tokensUsed int
	var processingTime int64
	var createdAt time.Time

	err := withDB(func(db *sql.DB) error {
		return db.QueryRow(`
			SELECT original_content, processed_content, provider, model, 
				   tokens_used, processing_time, created_at
			FROM ai_processing_records 
			WHERE content_hash = ? AND content_type = 'summarize'`, contentHash).Scan(
			&originalContent, &processedContent, &provider, &model,
			&tokensUsed, &processingTime, &createdAt)
	})

	if err != nil {
		return nil, false
	}

	result = SummaryResult{
		OriginalText:   originalContent,
		SummaryText:    processedContent,
		Provider:       provider,
		Model:          model,
		TokensUsed:     tokensUsed,
		ProcessingTime: processingTime,
		CreatedAt:      createdAt,
	}

	return &result, true
}

// CacheTranslation 缓存翻译结果
func (c *AICache) CacheTranslation(contentHash string, result *TranslateResult) error {
	return withDB(func(db *sql.DB) error {
		_, err := db.Exec(`
			INSERT OR REPLACE INTO ai_processing_records 
			(content_hash, content_type, original_content, processed_content, 
			 source_lang, target_lang, provider, model, tokens_used, processing_time)
			VALUES (?, 'translate', ?, ?, ?, ?, ?, ?, ?, ?)`,
			contentHash, result.OriginalText, result.TranslatedText,
			result.SourceLang, result.TargetLang, result.Provider,
			result.Model, result.TokensUsed, result.ProcessingTime)
		return err
	})
}

// CacheSummary 缓存摘要结果
func (c *AICache) CacheSummary(contentHash string, result *SummaryResult) error {
	return withDB(func(db *sql.DB) error {
		_, err := db.Exec(`
			INSERT OR REPLACE INTO ai_processing_records 
			(content_hash, content_type, original_content, processed_content, 
			 provider, model, tokens_used, processing_time)
			VALUES (?, 'summarize', ?, ?, ?, ?, ?, ?)`,
			contentHash, result.OriginalText, result.SummaryText,
			result.Provider, result.Model, result.TokensUsed, result.ProcessingTime)
		return err
	})
}

// UserAIPreferences 用户AI偏好设置
type UserAIPreferences struct {
	UserID           int64     `json:"user_id"`
	AutoTranslate    bool      `json:"auto_translate"`
	AutoSummarize    bool      `json:"auto_summarize"`
	PreferredLang    string    `json:"preferred_lang"`
	MaxSummaryLength int       `json:"max_summary_length"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// GetUserAIPreferences 获取用户AI偏好设置
func GetUserAIPreferences(userID int64) (*UserAIPreferences, error) {
	var preferences UserAIPreferences

	err := withDB(func(db *sql.DB) error {
		return db.QueryRow(`
			SELECT user_id, auto_translate, auto_summarize, preferred_lang, 
				   max_summary_length, created_at, updated_at
			FROM user_ai_preferences WHERE user_id = ?`, userID).Scan(
			&preferences.UserID, &preferences.AutoTranslate, &preferences.AutoSummarize,
			&preferences.PreferredLang, &preferences.MaxSummaryLength,
			&preferences.CreatedAt, &preferences.UpdatedAt)
	})

	if err == sql.ErrNoRows {
		// 返回默认设置
		return &UserAIPreferences{
			UserID:           userID,
			AutoTranslate:    false,
			AutoSummarize:    false,
			PreferredLang:    "zh-CN",
			MaxSummaryLength: 200,
			CreatedAt:        time.Now(),
			UpdatedAt:        time.Now(),
		}, nil
	}

	if err != nil {
		return nil, err
	}

	return &preferences, nil
}

// UpdateUserAIPreferences 更新用户AI偏好设置
func UpdateUserAIPreferences(preferences *UserAIPreferences) error {
	return withDB(func(db *sql.DB) error {
		// 检查记录是否存在
		var count int
		err := db.QueryRow("SELECT COUNT(*) FROM user_ai_preferences WHERE user_id = ?",
			preferences.UserID).Scan(&count)
		if err != nil {
			return err
		}

		preferences.UpdatedAt = time.Now()

		if count > 0 {
			// 更新现有记录
			_, err = db.Exec(`
				UPDATE user_ai_preferences 
				SET auto_translate = ?, auto_summarize = ?, preferred_lang = ?, 
					max_summary_length = ?, updated_at = ?
				WHERE user_id = ?`,
				preferences.AutoTranslate, preferences.AutoSummarize, preferences.PreferredLang,
				preferences.MaxSummaryLength, preferences.UpdatedAt, preferences.UserID)
		} else {
			// 插入新记录
			preferences.CreatedAt = time.Now()
			_, err = db.Exec(`
				INSERT INTO user_ai_preferences 
				(user_id, auto_translate, auto_summarize, preferred_lang, 
				 max_summary_length, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)`,
				preferences.UserID, preferences.AutoTranslate, preferences.AutoSummarize,
				preferences.PreferredLang, preferences.MaxSummaryLength,
				preferences.CreatedAt, preferences.UpdatedAt)
		}
		return err
	})
}

// generateContentHash 生成内容哈希
func generateContentHash(content string, contentType string, params ...string) string {
	// 将内容类型和参数合并
	allContent := contentType + "|" + content
	for _, param := range params {
		allContent += "|" + param
	}

	// 生成MD5哈希
	hasher := md5.New()
	hasher.Write([]byte(allContent))
	return hex.EncodeToString(hasher.Sum(nil))
}

// calculateCost 计算API调用成本
func calculateCost(tokensUsed int, provider string) float64 {
	// 简单的成本计算，可以根据不同提供商调整
	switch strings.ToLower(provider) {
	case "openai":
		// GPT-3.5-turbo 的大致费用：$0.002 / 1K tokens
		return float64(tokensUsed) * 0.002 / 1000
	default:
		return float64(tokensUsed) * 0.002 / 1000
	}
}

// ProcessedMessage 处理后的消息结构体
type ProcessedMessage struct {
	Original   *Message         // 原始消息
	Translated *TranslateResult // 翻译结果
	Summary    *SummaryResult   // 摘要结果
	HasAI      bool             // 是否包含AI处理结果
}

// FormatMessage 格式化处理后的消息
func (pm *ProcessedMessage) FormatMessage() string {
	var result strings.Builder

	// 原始标题和内容
	if pm.Original.Title != "" {
		result.WriteString(fmt.Sprintf("📰 **%s**\n\n", pm.Original.Title))
	}

	if pm.Original.Description != "" && !pm.HasAI {
		result.WriteString(pm.Original.Description)
		result.WriteString("\n\n")
	}

	// AI处理结果
	if pm.HasAI {
		if pm.Translated != nil {
			result.WriteString("🌐 **翻译**：\n")
			result.WriteString(pm.Translated.TranslatedText)
			result.WriteString("\n\n")
		}

		if pm.Summary != nil {
			result.WriteString("📄 **摘要**：\n")
			result.WriteString(pm.Summary.SummaryText)
			result.WriteString("\n\n")
		}

		// 如果有AI处理，也显示原文（折叠或简化）
		if pm.Original.Description != "" {
			result.WriteString("📝 **原文**：\n")
			// 限制原文显示长度
			originalText := pm.Original.Description
			if len(originalText) > 500 {
				originalText = originalText[:500] + "..."
			}
			result.WriteString(originalText)
			result.WriteString("\n\n")
		}
	}

	// 链接
	if pm.Original.Link != "" {
		result.WriteString(fmt.Sprintf("🔗 [查看原文](%s)", pm.Original.Link))
	}

	return result.String()
}

// AIUsageStats AI使用统计
type AIUsageStats struct {
	Date           string    `json:"date"`
	TranslateCount int       `json:"translate_count"`
	SummarizeCount int       `json:"summarize_count"`
	TotalTokens    int       `json:"total_tokens"`
	TotalCost      float64   `json:"total_cost"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// GetAIUsageStats 获取AI使用统计
func GetAIUsageStats(days int) ([]AIUsageStats, error) {
	var stats []AIUsageStats

	err := withDB(func(db *sql.DB) error {
		rows, err := db.Query(`
			SELECT date, translate_count, summarize_count, total_tokens, total_cost, updated_at
			FROM ai_usage_stats 
			ORDER BY date DESC 
			LIMIT ?`, days)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var stat AIUsageStats
			err := rows.Scan(&stat.Date, &stat.TranslateCount, &stat.SummarizeCount,
				&stat.TotalTokens, &stat.TotalCost, &stat.UpdatedAt)
			if err != nil {
				continue
			}
			stats = append(stats, stat)
		}
		return nil
	})

	return stats, err
}

// FormatAIStatsReport 格式化AI统计报告
func FormatAIStatsReport(stats []AIUsageStats) string {
	if len(stats) == 0 {
		return "📊 暂无AI使用统计数据"
	}

	var result strings.Builder
	result.WriteString("📊 **AI使用统计报告**\n\n")

	totalTranslate := 0
	totalSummarize := 0
	totalTokens := 0
	totalCost := 0.0

	for _, stat := range stats {
		totalTranslate += stat.TranslateCount
		totalSummarize += stat.SummarizeCount
		totalTokens += stat.TotalTokens
		totalCost += stat.TotalCost

		result.WriteString(fmt.Sprintf("📅 **%s**\n", stat.Date))
		result.WriteString(fmt.Sprintf("  🌐 翻译: %d次\n", stat.TranslateCount))
		result.WriteString(fmt.Sprintf("  📄 摘要: %d次\n", stat.SummarizeCount))
		result.WriteString(fmt.Sprintf("  🎯 Token: %d\n", stat.TotalTokens))
		result.WriteString(fmt.Sprintf("  💰 费用: $%.4f\n\n", stat.TotalCost))
	}

	result.WriteString("📈 **总计统计**\n")
	result.WriteString(fmt.Sprintf("🌐 总翻译次数: %d\n", totalTranslate))
	result.WriteString(fmt.Sprintf("📄 总摘要次数: %d\n", totalSummarize))
	result.WriteString(fmt.Sprintf("🎯 总Token使用: %d\n", totalTokens))
	result.WriteString(fmt.Sprintf("💰 总费用: $%.4f", totalCost))

	return result.String()
}
