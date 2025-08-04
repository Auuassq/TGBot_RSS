package main

import (
	"context"
	"fmt"
	"time"
)

// Language 语言结构体
type Language struct {
	Code string // 语言代码，如 "zh-CN", "en", "ja"
	Name string // 语言名称，如 "中文", "English", "日本語"
}

// TranslateResult 翻译结果结构体
type TranslateResult struct {
	OriginalText   string    // 原文
	TranslatedText string    // 译文
	SourceLang     string    // 源语言代码
	TargetLang     string    // 目标语言代码
	Provider       string    // AI服务提供商
	Model          string    // 使用的模型
	TokensUsed     int       // 使用的token数量
	ProcessingTime int64     // 处理时间（毫秒）
	CreatedAt      time.Time // 创建时间
}

// SummaryResult 摘要结果结构体
type SummaryResult struct {
	OriginalText   string    // 原文
	SummaryText    string    // 摘要文本
	MaxLength      int       // 最大摘要长度
	MinLength      int       // 最小内容长度
	Provider       string    // AI服务提供商
	Model          string    // 使用的模型
	TokensUsed     int       // 使用的token数量
	ProcessingTime int64     // 处理时间（毫秒）
	CreatedAt      time.Time // 创建时间
}

// AIError AI服务错误类型
type AIError struct {
	Code     string // 错误代码
	Message  string // 错误消息
	Provider string // 服务提供商
	Type     string // 错误类型：network, api, quota, invalid_request等
}

func (e *AIError) Error() string {
	return fmt.Sprintf("[%s] %s: %s", e.Provider, e.Code, e.Message)
}

// NewAIError 创建AI错误
func NewAIError(provider, code, message, errorType string) *AIError {
	return &AIError{
		Code:     code,
		Message:  message,
		Provider: provider,
		Type:     errorType,
	}
}

// AIService AI服务接口
// 定义了所有AI服务提供商需要实现的方法
type AIService interface {
	// Translate 翻译文本
	// ctx: 上下文，用于超时控制
	// text: 要翻译的文本
	// sourceLang: 源语言代码，空字符串表示自动检测
	// targetLang: 目标语言代码
	// 返回: 翻译结果和错误
	Translate(ctx context.Context, text, sourceLang, targetLang string) (*TranslateResult, error)

	// Summarize 生成摘要
	// ctx: 上下文，用于超时控制
	// text: 要摘要的文本
	// maxLength: 最大摘要长度
	// minLength: 最小内容长度（如果原文短于此长度则不摘要）
	// 返回: 摘要结果和错误
	Summarize(ctx context.Context, text string, maxLength, minLength int) (*SummaryResult, error)

	// GetName 获取服务提供商名称
	GetName() string

	// GetModel 获取当前使用的模型
	GetModel() string

	// IsAvailable 检查服务是否可用
	IsAvailable(ctx context.Context) bool

	// GetSupportedLanguages 获取支持的语言列表
	GetSupportedLanguages() []Language
}

// AIServiceConfig AI服务配置
type AIServiceConfig struct {
	Provider    string            // 服务提供商名称
	APIKey      string            // API密钥
	BaseURL     string            // API基础URL
	Model       string            // 使用的模型
	ProxyURL    string            // 代理URL
	MaxTokens   int               // 最大token数
	Temperature float32           // 温度参数
	Timeout     time.Duration     // 请求超时时间
	Extra       map[string]string // 额外配置参数
}

// AIServiceManager AI服务管理器
type AIServiceManager struct {
	services map[string]AIService // 注册的服务
	config   *AIServiceConfig     // 当前配置
	current  AIService           // 当前使用的服务
}

// NewAIServiceManager 创建AI服务管理器
func NewAIServiceManager() *AIServiceManager {
	return &AIServiceManager{
		services: make(map[string]AIService),
	}
}

// RegisterService 注册AI服务
func (m *AIServiceManager) RegisterService(name string, service AIService) {
	m.services[name] = service
}

// SetConfig 设置配置并切换到指定服务
func (m *AIServiceManager) SetConfig(config *AIServiceConfig) error {
	service, exists := m.services[config.Provider]
	if !exists {
		return fmt.Errorf("不支持的AI服务提供商: %s", config.Provider)
	}

	m.config = config
	m.current = service
	return nil
}

// GetCurrentService 获取当前服务
func (m *AIServiceManager) GetCurrentService() AIService {
	return m.current
}

// GetConfig 获取当前配置
func (m *AIServiceManager) GetConfig() *AIServiceConfig {
	return m.config
}

// IsConfigured 检查是否已配置
func (m *AIServiceManager) IsConfigured() bool {
	return m.config != nil && m.current != nil
}

// GetAvailableProviders 获取可用的服务提供商列表
func (m *AIServiceManager) GetAvailableProviders() []string {
	var providers []string
	for name := range m.services {
		providers = append(providers, name)
	}
	return providers
}

// 预定义的常用语言
var (
	SupportedLanguages = []Language{
		{Code: "zh-CN", Name: "中文（简体）"},
		{Code: "zh-TW", Name: "中文（繁体）"},
		{Code: "en", Name: "English"},
		{Code: "ja", Name: "日本語"},
		{Code: "ko", Name: "한국어"},
		{Code: "es", Name: "Español"},
		{Code: "fr", Name: "Français"},
		{Code: "de", Name: "Deutsch"},
		{Code: "ru", Name: "Русский"},
		{Code: "pt", Name: "Português"},
		{Code: "it", Name: "Italiano"},
		{Code: "ar", Name: "العربية"},
		{Code: "hi", Name: "हिन्दी"},
		{Code: "th", Name: "ไทย"},
		{Code: "vi", Name: "Tiếng Việt"},
	}
)

// GetLanguageByCode 根据代码获取语言信息
func GetLanguageByCode(code string) *Language {
	for _, lang := range SupportedLanguages {
		if lang.Code == code {
			return &lang
		}
	}
	return nil
}

// IsValidLanguageCode 检查语言代码是否有效
func IsValidLanguageCode(code string) bool {
	return GetLanguageByCode(code) != nil
}