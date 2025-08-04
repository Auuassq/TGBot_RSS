package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OpenAIRequest OpenAI API请求结构体
type OpenAIRequest struct {
	Model       string                 `json:"model"`
	Messages    []OpenAIMessage        `json:"messages"`
	MaxTokens   int                    `json:"max_tokens,omitempty"`
	Temperature float32                `json:"temperature,omitempty"`
	Stream      bool                   `json:"stream"`
	Extra       map[string]interface{} `json:"-"` // 额外参数
}

// OpenAIMessage OpenAI消息结构体
type OpenAIMessage struct {
	Role    string `json:"role"`    // system, user, assistant
	Content string `json:"content"` // 消息内容
}

// OpenAIResponse OpenAI API响应结构体
type OpenAIResponse struct {
	ID      string           `json:"id"`
	Object  string           `json:"object"`
	Created int64            `json:"created"`
	Model   string           `json:"model"`
	Choices []OpenAIChoice   `json:"choices"`
	Usage   OpenAIUsage      `json:"usage"`
	Error   *OpenAIErrorResp `json:"error,omitempty"`
}

// OpenAIChoice 选择结构体
type OpenAIChoice struct {
	Index        int           `json:"index"`
	Message      OpenAIMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

// OpenAIUsage 使用统计结构体
type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// OpenAIErrorResp OpenAI错误响应结构体
type OpenAIErrorResp struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

// AIClient AI HTTP客户端
type AIClient struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	proxyURL   string
	timeout    time.Duration
}

// NewAIClient 创建AI客户端
func NewAIClient(baseURL, apiKey, proxyURL string, timeout time.Duration) *AIClient {
	client := &AIClient{
		baseURL:  strings.TrimSuffix(baseURL, "/"),
		apiKey:   apiKey,
		proxyURL: proxyURL,
		timeout:  timeout,
	}
	client.httpClient = client.createHTTPClient()
	return client
}

// createHTTPClient 创建HTTP客户端
func (c *AIClient) createHTTPClient() *http.Client {
	transport := &http.Transport{
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: 10 * time.Second,
	}

	// 配置代理
	if c.proxyURL != "" {
		if proxyURLParsed, err := url.Parse(c.proxyURL); err == nil {
			transport.Proxy = http.ProxyURL(proxyURLParsed)
		}
	}

	return &http.Client{
		Timeout:   c.timeout,
		Transport: transport,
	}
}

// CallAPI 调用OpenAI兼容的API
func (c *AIClient) CallAPI(ctx context.Context, endpoint string, request interface{}) (*OpenAIResponse, error) {
	// 序列化请求
	jsonData, err := json.Marshal(request)
	if err != nil {
		return nil, NewAIError("openai", "json_marshal_error", 
			fmt.Sprintf("序列化请求失败: %v", err), "invalid_request")
	}

	// 创建HTTP请求
	fullURL := fmt.Sprintf("%s%s", c.baseURL, endpoint)
	req, err := http.NewRequestWithContext(ctx, "POST", fullURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, NewAIError("openai", "request_creation_error",
			fmt.Sprintf("创建请求失败: %v", err), "network")
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))
	req.Header.Set("User-Agent", "TGBot-RSS-AI/1.0")

	// 发送请求
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, NewAIError("openai", "network_error",
			fmt.Sprintf("网络请求失败: %v", err), "network")
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, NewAIError("openai", "response_read_error",
			fmt.Sprintf("读取响应失败: %v", err), "network")
	}

	// 解析响应
	var response OpenAIResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, NewAIError("openai", "json_unmarshal_error",
			fmt.Sprintf("解析响应失败: %v, 响应内容: %s", err, string(body)), "api")
	}

	// 检查API错误
	if response.Error != nil {
		errorType := "api"
		if strings.Contains(response.Error.Type, "quota") || strings.Contains(response.Error.Code, "quota") {
			errorType = "quota"
		} else if strings.Contains(response.Error.Type, "invalid") {
			errorType = "invalid_request"
		}
		
		return nil, NewAIError("openai", response.Error.Code,
			response.Error.Message, errorType)
	}

	// 检查HTTP状态码
	if resp.StatusCode != http.StatusOK {
		return nil, NewAIError("openai", fmt.Sprintf("http_%d", resp.StatusCode),
			fmt.Sprintf("HTTP错误: %d, 响应: %s", resp.StatusCode, string(body)), "api")
	}

	return &response, nil
}

// OpenAIAdapter OpenAI适配器
type OpenAIAdapter struct {
	client      *AIClient
	config      *AIServiceConfig
	name        string
	model       string
	maxTokens   int
	temperature float32
}

// NewOpenAIAdapter 创建OpenAI适配器
func NewOpenAIAdapter(config *AIServiceConfig) *OpenAIAdapter {
	// 设置默认值
	if config.BaseURL == "" {
		config.BaseURL = "https://api.openai.com/v1"
	}
	if config.Model == "" {
		config.Model = "gpt-3.5-turbo"
	}
	if config.MaxTokens == 0 {
		config.MaxTokens = 1000
	}
	if config.Temperature == 0 {
		config.Temperature = 0.3
	}
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}

	client := NewAIClient(config.BaseURL, config.APIKey, config.ProxyURL, config.Timeout)

	return &OpenAIAdapter{
		client:      client,
		config:      config,
		name:        config.Provider,
		model:       config.Model,
		maxTokens:   config.MaxTokens,
		temperature: config.Temperature,
	}
}

// GetName 获取服务提供商名称
func (a *OpenAIAdapter) GetName() string {
	return a.name
}

// GetModel 获取当前使用的模型
func (a *OpenAIAdapter) GetModel() string {
	return a.model
}

// IsAvailable 检查服务是否可用
func (a *OpenAIAdapter) IsAvailable(ctx context.Context) bool {
	// 发送简单的测试请求
	request := OpenAIRequest{
		Model: a.model,
		Messages: []OpenAIMessage{
			{Role: "user", Content: "Hello"},
		},
		MaxTokens:   10,
		Temperature: 0.1,
		Stream:      false,
	}

	_, err := a.client.CallAPI(ctx, "/chat/completions", request)
	return err == nil
}

// GetSupportedLanguages 获取支持的语言列表
func (a *OpenAIAdapter) GetSupportedLanguages() []Language {
	return SupportedLanguages
}

// Translate 翻译文本
func (a *OpenAIAdapter) Translate(ctx context.Context, text, sourceLang, targetLang string) (*TranslateResult, error) {
	startTime := time.Now()

	// 构建提示词
	var prompt string
	if sourceLang == "" {
		sourceLang = "auto"
		prompt = fmt.Sprintf("请将以下文本翻译为%s，只返回翻译结果，不要添加任何解释或格式：\n\n%s", 
			getLanguageName(targetLang), text)
	} else {
		prompt = fmt.Sprintf("请将以下%s文本翻译为%s，只返回翻译结果，不要添加任何解释或格式：\n\n%s", 
			getLanguageName(sourceLang), getLanguageName(targetLang), text)
	}

	// 构建请求
	request := OpenAIRequest{
		Model: a.model,
		Messages: []OpenAIMessage{
			{
				Role:    "system",
				Content: "你是一个专业的翻译助手，请准确翻译用户提供的文本。",
			},
			{
				Role:    "user",
				Content: prompt,
			},
		},
		MaxTokens:   a.maxTokens,
		Temperature: a.temperature,
		Stream:      false,
	}

	// 调用API
	response, err := a.client.CallAPI(ctx, "/chat/completions", request)
	if err != nil {
		return nil, err
	}

	// 检查响应
	if len(response.Choices) == 0 {
		return nil, NewAIError(a.name, "empty_response", "API返回空响应", "api")
	}

	translatedText := strings.TrimSpace(response.Choices[0].Message.Content)
	processingTime := time.Since(startTime).Milliseconds()

	return &TranslateResult{
		OriginalText:   text,
		TranslatedText: translatedText,
		SourceLang:     sourceLang,
		TargetLang:     targetLang,
		Provider:       a.name,
		Model:          a.model,
		TokensUsed:     response.Usage.TotalTokens,
		ProcessingTime: processingTime,
		CreatedAt:      time.Now(),
	}, nil
}

// Summarize 生成摘要
func (a *OpenAIAdapter) Summarize(ctx context.Context, text string, maxLength, minLength int) (*SummaryResult, error) {
	startTime := time.Now()

	// 检查文本长度
	if len(text) < minLength {
		return nil, NewAIError(a.name, "text_too_short", 
			fmt.Sprintf("文本长度%d小于最小长度%d", len(text), minLength), "invalid_request")
	}

	// 构建提示词
	prompt := fmt.Sprintf(`请为以下文本生成一个简洁的摘要，要求：
1. 摘要长度不超过%d个字符
2. 保留主要信息和关键点
3. 使用简洁明了的语言
4. 只返回摘要内容，不要添加任何解释或格式

原文：
%s`, maxLength, text)

	// 构建请求
	request := OpenAIRequest{
		Model: a.model,
		Messages: []OpenAIMessage{
			{
				Role:    "system", 
				Content: "你是一个专业的文本摘要助手，擅长提取文本的核心信息并生成简洁的摘要。",
			},
			{
				Role:    "user",
				Content: prompt,
			},
		},
		MaxTokens:   a.maxTokens,
		Temperature: a.temperature,
		Stream:      false,
	}

	// 调用API
	response, err := a.client.CallAPI(ctx, "/chat/completions", request)
	if err != nil {
		return nil, err
	}

	// 检查响应
	if len(response.Choices) == 0 {
		return nil, NewAIError(a.name, "empty_response", "API返回空响应", "api")
	}

	summaryText := strings.TrimSpace(response.Choices[0].Message.Content)
	processingTime := time.Since(startTime).Milliseconds()

	return &SummaryResult{
		OriginalText:   text,
		SummaryText:    summaryText,
		MaxLength:      maxLength,
		MinLength:      minLength,
		Provider:       a.name,
		Model:          a.model,
		TokensUsed:     response.Usage.TotalTokens,
		ProcessingTime: processingTime,
		CreatedAt:      time.Now(),
	}, nil
}

// getLanguageName 根据语言代码获取语言名称
func getLanguageName(code string) string {
	if lang := GetLanguageByCode(code); lang != nil {
		return lang.Name
	}
	return code
}