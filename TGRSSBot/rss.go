package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/mmcdole/gofeed"
)

// initializeAIService 初始化AI服务
func initializeAIService() AIService {
	if globalConfig.AI == nil || !globalConfig.AI.Enabled {
		return nil
	}

	// 创建AI服务配置
	config := &AIServiceConfig{
		Provider:    globalConfig.AI.Provider,
		APIKey:      globalConfig.AI.APIKey,
		BaseURL:     globalConfig.AI.BaseURL,
		Model:       globalConfig.AI.Model,
		ProxyURL:    globalConfig.AI.ProxyURL,
		MaxTokens:   globalConfig.AI.MaxTokens,
		Temperature: globalConfig.AI.Temperature,
		Timeout:     30 * time.Second,
	}

	// 根据提供商创建相应的服务
	switch strings.ToLower(config.Provider) {
	case "openai":
		return NewOpenAIAdapter(config)
	default:
		logMessage("warn", fmt.Sprintf("不支持的AI服务提供商: %s", config.Provider))
		return nil
	}
}

// processMessageWithAI 使用AI处理消息
func processMessageWithAI(ctx context.Context, aiHandler *AIHandler, msg *Message, userPrefs *UserAIPreferences) (*ProcessedMessage, error) {
	processed := &ProcessedMessage{
		Original: msg,
		HasAI:    false,
	}

	// 准备内容文本用于AI处理（去掉HTML标签）
	content := cleanHTMLContent(msg.Title + " " + msg.Description)
	
	// 如果内容太短，不进行AI处理
	if len(content) < 50 {
		return processed, nil
	}

	var hasAIProcessing bool

	// 处理翻译
	if userPrefs.AutoTranslate && globalConfig.AI.Features.Translation.Enabled {
		if translateResult, err := aiHandler.HandleTranslateRequest(ctx, content, "", userPrefs.PreferredLang); err == nil {
			processed.Translated = translateResult
			hasAIProcessing = true
			logMessage("debug", "AI翻译完成")
		} else {
			logMessage("warn", fmt.Sprintf("AI翻译失败: %v", err))
		}
	}

	// 处理摘要
	if userPrefs.AutoSummarize && globalConfig.AI.Features.Summarization.Enabled {
		maxLength := userPrefs.MaxSummaryLength
		if maxLength == 0 {
			maxLength = globalConfig.AI.Features.Summarization.MaxLength
		}
		minLength := globalConfig.AI.Features.Summarization.MinLength

		if summaryResult, err := aiHandler.HandleSummarizeRequest(ctx, content, maxLength, minLength); err == nil {
			processed.Summary = summaryResult
			hasAIProcessing = true
			logMessage("debug", "AI摘要完成")
		} else {
			logMessage("warn", fmt.Sprintf("AI摘要失败: %v", err))
		}
	}

	processed.HasAI = hasAIProcessing
	return processed, nil
}

// sendProcessedMessage 发送处理后的消息
func sendProcessedMessage(userID int64, sub Subscription, processedMsg *ProcessedMessage, formattedKeywords string) {
	msg := processedMsg.Original
	formattedDate := msg.PubDate.In(time.FixedZone("CST", 8*60*60)).Format("2006-01-02 15:04:05")
	
	var htmlMessage string
	
	if sub.Channel == 1 {
		// 频道模式：显示完整内容
		imageURL := extractImageURL(msg.Description)
		
		if processedMsg.HasAI {
			// 使用AI处理后的格式
			htmlMessage = formatAIEnhancedMessage(sub.Name, formattedKeywords, formattedDate, processedMsg)
		} else {
			// 使用原始格式
			cleanDescription := cleanHTMLContent(msg.Description)
			htmlMessage = fmt.Sprintf("👋 %s: %s\n🕒 %s\n%s\n", sub.Name, formattedKeywords, formattedDate, cleanDescription)
		}
		
		// 根据是否有图片决定发送方式
		if imageURL != "" {
			go sendPhotoMessage(userID, imageURL, htmlMessage)
		} else {
			go sendHTMLMessage(userID, htmlMessage)
		}
	} else {
		// 链接模式：显示标题和链接
		htmlMessage = fmt.Sprintf("📌 %s\n🔖 关键词: %s\n🕒 %s", msg.Title, formattedKeywords, formattedDate)
		
		if processedMsg.HasAI {
			// 添加AI处理结果
			if processedMsg.Translated != nil {
				htmlMessage += fmt.Sprintf("\n🌐 翻译: %s", processedMsg.Translated.TranslatedText)
			}
			if processedMsg.Summary != nil {
				htmlMessage += fmt.Sprintf("\n📄 摘要: %s", processedMsg.Summary.SummaryText)
			}
		}
		
		htmlMessage += fmt.Sprintf("\n🔗 %s", msg.Link)
		go sendHTMLMessage(userID, htmlMessage)
	}
}

// formatAIEnhancedMessage 格式化AI增强的消息
func formatAIEnhancedMessage(sourceName, formattedKeywords, formattedDate string, processedMsg *ProcessedMessage) string {
	var result strings.Builder
	
	// 头部信息
	result.WriteString(fmt.Sprintf("👋 %s: %s\n🕒 %s\n\n", sourceName, formattedKeywords, formattedDate))
	
	// AI处理结果
	if processedMsg.Translated != nil {
		result.WriteString("🌐 <b>翻译</b>：\n")
		result.WriteString(processedMsg.Translated.TranslatedText)
		result.WriteString("\n\n")
	}
	
	if processedMsg.Summary != nil {
		result.WriteString("📄 <b>摘要</b>：\n")
		result.WriteString(processedMsg.Summary.SummaryText)
		result.WriteString("\n\n")
	}
	
	// 原文（如果有AI处理则折叠显示）
	if processedMsg.HasAI && processedMsg.Original.Description != "" {
		result.WriteString("📝 <b>原文</b>：\n")
		originalText := cleanHTMLContent(processedMsg.Original.Description)
		// 限制原文显示长度
		if len(originalText) > 300 {
			originalText = originalText[:300] + "..."
		}
		result.WriteString(originalText)
		result.WriteString("\n")
	} else if !processedMsg.HasAI {
		// 没有AI处理时显示完整原文
		cleanDescription := cleanHTMLContent(processedMsg.Original.Description)
		result.WriteString(cleanDescription)
		result.WriteString("\n")
	}
	
	return result.String()
}

// 获取所有订阅
func getSubscriptions(db *sql.DB) ([]Subscription, error) {
	rows, err := db.Query("SELECT subscription_id, rss_url, rss_name, users, channel FROM subscriptions")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subscriptions []Subscription
	for rows.Next() {
		var sub Subscription
		var usersStr string
		var channel int

		if err := rows.Scan(&sub.ID, &sub.URL, &sub.Name, &usersStr, &channel); err != nil {
			logMessage("error", fmt.Sprintf("读取订阅失败: %v", err))
			continue
		}

		// 解析用户ID列表
		sub.Users = parseUserIDs(usersStr)
		sub.Channel = channel
		subscriptions = append(subscriptions, sub)
	}

	return subscriptions, nil
}

// 解析用户ID字符串
func parseUserIDs(usersStr string) []int64 {
	usersStr = strings.Trim(usersStr, "[] ")
	if usersStr == "" {
		return nil
	}

	var userIDs []int64
	for _, idStr := range strings.Split(usersStr, ",") {
		var id int64
		if n, _ := fmt.Sscanf(strings.TrimSpace(idStr), "%d", &id); n == 1 && id > 0 {
			userIDs = append(userIDs, id)
		}
	}
	return userIDs
}

// 获取用户关键词
func getUserKeywords(db *sql.DB) (map[int64][]string, error) {
	rows, err := db.Query("SELECT user_id, keywords FROM user_keywords")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	userKeywords := make(map[int64][]string)
	for rows.Next() {
		var userID int64
		var keywordsStr string

		if err := rows.Scan(&userID, &keywordsStr); err != nil {
			continue
		}

		// 解析关键词
		keywords := parseKeywords(keywordsStr)
		if len(keywords) > 0 {
			userKeywords[userID] = keywords
		}
	}

	return userKeywords, nil
}

// 解析关键词字符串
func parseKeywords(keywordsStr string) []string {
	keywordsStr = strings.TrimSpace(keywordsStr)
	if keywordsStr == "" {
		return nil
	}

	// 如果是 JSON 数组格式
	if strings.HasPrefix(keywordsStr, "[") && strings.HasSuffix(keywordsStr, "]") {
		var keywords []string
		if err := json.Unmarshal([]byte(keywordsStr), &keywords); err == nil {
			return keywords
		}
	}

	// 如果不是 JSON 格式，按照逗号分割
	var keywords []string
	for _, kw := range strings.Split(keywordsStr, ",") {
		kw = strings.TrimSpace(kw)
		if kw != "" {
			keywords = append(keywords, kw)
		}
	}
	return keywords
}

// 获取RSS内容
func fetchRSS(db *sql.DB, sub Subscription, client *http.Client) ([]Message, error) {
	parser := gofeed.NewParser()
	parser.Client = client

	// 获取RSS内容
	feed, err := parser.ParseURL(sub.URL)
	if err != nil {
		return nil, err
	}

	if len(feed.Items) == 0 {
		return nil, nil
	}

	// 获取上次更新时间
	lastUpdateTime, err := getLastUpdateTime(db, sub.Name)
	if err != nil {
		logMessage("error", fmt.Sprintf("获取更新时间失败: %v", err))
		lastUpdateTime = time.Time{} // 使用零时间
	}

	// 处理新消息
	var messages []Message
	var latestTime time.Time

	for _, item := range feed.Items {
		pubTime := getItemTime(item)
		if pubTime.After(latestTime) {
			latestTime = pubTime
		}

		// 只添加新的内容
		if pubTime.After(lastUpdateTime) {
			messages = append(messages, Message{
				Title:       item.Title,
				Description: item.Description,
				Link:        item.Link,
				PubDate:     pubTime,
			})
		}
	}

	// 更新最后更新时间
	if !latestTime.IsZero() {
		updateLastTime(db, sub.Name, latestTime, feed.Items[0].Title)
	}

	return messages, nil
}

// 获取RSS项目的时间
func getItemTime(item *gofeed.Item) time.Time {
	if item.PublishedParsed != nil {
		return item.PublishedParsed.UTC()
	}
	if item.UpdatedParsed != nil {
		return item.UpdatedParsed.UTC()
	}
	return time.Now().UTC()
}

// 获取上次更新时间
func getLastUpdateTime(db *sql.DB, rssName string) (time.Time, error) {
	var timeStr string
	err := db.QueryRow("SELECT last_update_time FROM feed_data WHERE rss_name = ?", rssName).Scan(&timeStr)

	if err == sql.ErrNoRows {
		// 首次运行，插入记录
		_, err = db.Exec("INSERT INTO feed_data (rss_name, last_update_time, latest_title) VALUES (?, ?, ?)",
			rssName, time.Now().Format("2006-01-02 15:04:05"), "")
		return time.Time{}, err
	}

	if err != nil {
		return time.Time{}, err
	}

	return time.Parse("2006-01-02 15:04:05", timeStr)
}

// 更新最后更新时间
func updateLastTime(db *sql.DB, rssName string, updateTime time.Time, title string) {
	_, err := db.Exec("UPDATE feed_data SET last_update_time = ?, latest_title = ? WHERE rss_name = ?",
		updateTime.Format("2006-01-02 15:04:05"), title, rssName)
	if err != nil {
		logMessage("error", fmt.Sprintf("更新时间失败: %v", err))
	}
}

// 检查消息是否匹配关键词，返回匹配到的关键词列表
func matchesKeywords(msg Message, keywords []string) []string {
	if len(keywords) == 0 {
		return nil
	}

	var matchedKeywords []string
	var blockedKeywords []string
	content := strings.ToLower(msg.Title + " " + msg.Description)

	// 首先检查是否命中屏蔽词
	for _, keyword := range keywords {
		keyword = strings.TrimSpace(keyword)
		if keyword == "" {
			continue
		}
		// 检查是否是屏蔽关键词
		isBlockKeyword := strings.HasPrefix(keyword, "-")
		if isBlockKeyword {
			keyword = strings.TrimPrefix(keyword, "-")
			//fmt.Println("屏蔽关键词:", keyword)
		}

		// 将关键词转为小写
		lowerKeyword := strings.ToLower(keyword)

		// 检查是否包含通配符
		if strings.Contains(lowerKeyword, "*") {
			//fmt.Println(lowerKeyword)
			// 将通配符转换为正则表达式
			pattern := strings.ReplaceAll(lowerKeyword, "*", ".*")
			pattern = "^.*" + pattern + ".*$"

			// 编译正则表达式
			re, err := regexp.Compile(pattern)
			if err == nil && re.MatchString(content) {
				if isBlockKeyword {
					blockedKeywords = append(blockedKeywords, keyword)
				} else {
					matchedKeywords = append(matchedKeywords, keyword)
				}
				continue
			}
		}

		// 如果没有通配符或正则表达式失败，使用普通匹配
		if strings.Contains(content, lowerKeyword) {
			if isBlockKeyword {
				blockedKeywords = append(blockedKeywords, keyword)
			} else {
				matchedKeywords = append(matchedKeywords, keyword)
			}
		}
	}

	// 如果命中任何屏蔽词，则返回空
	if len(blockedKeywords) > 0 {
		logMessage("debug", fmt.Sprintf("消息被屏蔽词[%s]过滤: %s",
			strings.Join(blockedKeywords, ", "), msg.Title))
		return nil
	}

	return matchedKeywords
}

// 处理单个订阅
func processSubscription(db *sql.DB, sub Subscription, userKeywords map[int64][]string, client *http.Client) {
	if cyclenum == 0 {
		logMessage("info", fmt.Sprintf("处理订阅: %s (%s)", sub.Name, sub.URL))
	}
	messages, err := fetchRSS(db, sub, client)
	if err != nil {
		logMessage("error", fmt.Sprintf("获取RSS失败 %s: %v", sub.Name, err))
		return
	}

	if len(messages) == 0 {
		logMessage("debug", fmt.Sprintf("订阅 %s 无新内容", sub.Name))
		return
	}

	// 初始化AI处理器（如果启用）
	var aiHandler *AIHandler
	if globalConfig.AI != nil && globalConfig.AI.Enabled {
		if aiService := initializeAIService(); aiService != nil {
			aiHandler = NewAIHandler(aiService, db)
		}
	}

	// 处理推送
	pushCount := 0
	for _, msg := range messages {
		for _, userID := range sub.Users {
			keywords := userKeywords[userID]
			if len(keywords) == 0 {
				continue // 用户没有设置关键词且不是全量推送，跳过
			}
			matchedKeywords := matchesKeywords(msg, keywords)

			// 如果匹配到关键词或是全量推送，则发送消息
			if len(matchedKeywords) > 0 {
				pushCount++
				logMessage("debug", fmt.Sprintf("关键词[%s]匹配 推送给用户 %d: %s",
					strings.Join(matchedKeywords, ", "), userID, msg.Title))
				
				recordPush(sub.Name)
				
				// 获取用户AI偏好设置
				var processedMsg *ProcessedMessage
				if aiHandler != nil {
					userPrefs, err := GetUserAIPreferences(userID)
					if err != nil {
						logMessage("warn", fmt.Sprintf("获取用户AI偏好失败: %v", err))
						// 使用默认偏好
						userPrefs = &UserAIPreferences{
							UserID:           userID,
							AutoTranslate:    false,
							AutoSummarize:    false,
							PreferredLang:    "zh-CN",
							MaxSummaryLength: 200,
						}
					}
					
					// 使用AI处理消息（如果用户启用了AI功能）
					if userPrefs.AutoTranslate || userPrefs.AutoSummarize {
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						defer cancel()
						
						processedMsg, err = processMessageWithAI(ctx, aiHandler, &msg, userPrefs)
						if err != nil {
							logMessage("warn", fmt.Sprintf("AI处理消息失败: %v", err))
							// 继续使用原始消息
							processedMsg = &ProcessedMessage{Original: &msg, HasAI: false}
						}
					} else {
						// 用户未启用AI功能，使用原始消息
						processedMsg = &ProcessedMessage{Original: &msg, HasAI: false}
					}
				} else {
					// AI未启用，使用原始消息
					processedMsg = &ProcessedMessage{Original: &msg, HasAI: false}
				}
				
				// 格式化关键词列表，每个关键词单独用code标签包裹
				var formattedKeywords string
				if len(matchedKeywords) > 0 {
					keywordCodes := make([]string, len(matchedKeywords))
					for i, kw := range matchedKeywords {
						keywordCodes[i] = fmt.Sprintf("<code>%s</code>", kw)
					}
					formattedKeywords = strings.Join(keywordCodes, " ")
				}
				
				// 构造和发送消息
				sendProcessedMessage(userID, sub, processedMsg, formattedKeywords)
				
				// 给管理员发送简化版本
				if userID == globalConfig.ADMINIDS {
					formattedDate := msg.PubDate.In(time.FixedZone("CST", 8*60*60)).Format("2006-01-02 15:04:05")
					var otherpush string
					if sub.Channel == 1 {
						cleanDescription := cleanHTMLContent(msg.Description)
						otherpush = fmt.Sprintf("👋 %s\n🕒 %s\n%s", sub.Name, formattedDate, cleanDescription)
					} else {
						otherpush = fmt.Sprintf("📌 %s\n🕒 %s\n🔗 %s", msg.Title, formattedDate, msg.Link)
					}
					go sendother(otherpush)
				}
			}
		}
	}
	logMessage("info", fmt.Sprintf("订阅 %s 完成，推送 %d 条消息", sub.Name, pushCount))
}

// 检查所有RSS订阅
func checkAllRSS(db *sql.DB) {
	db, err := sql.Open("sqlite3", "tgbot.db")
	if err != nil {
		logMessage("error", fmt.Sprintf("连接数据库失败: %v", err))
		os.Exit(1)
	}
	defer db.Close()
	startTime := time.Now()
	resetPushStatsIfNeeded()
	logMessage("info", "开始检查RSS订阅...")

	// 获取数据
	subscriptions, err := getSubscriptions(db)
	if err != nil {
		logMessage("error", fmt.Sprintf("获取订阅失败: %v", err))
		return
	}

	if len(subscriptions) == 0 {
		logMessage("info", "没有找到RSS订阅")
		return
	}

	userKeywords, err := getUserKeywords(db)
	if err != nil {
		logMessage("error", fmt.Sprintf("获取用户关键词失败: %v", err))
		return
	}

	client := createHTTPClient(globalConfig.ProxyURL)

	// 并发处理订阅
	var wg sync.WaitGroup
	for _, sub := range subscriptions {
		wg.Add(1)
		go func(sub Subscription) {
			defer wg.Done()
			processSubscription(db, sub, userKeywords, client)
		}(sub)
	}

	wg.Wait()
	logMessage("info", fmt.Sprintf("RSS检查完成，耗时: %v", time.Since(startTime)))
	cyclenum = 1
	// 打印当前的推送统计
	//stats := GetPushStatsInfo()
	//if DailyPushStats.TotalPush > 0 {
	//	logMessage("info", stats)
	//}
}

// extractImageURL 从HTML内容中提取第一个图片URL
func extractImageURL(htmlContent string) string {
	// 1. 正则表达式匹配img标签的src属性
	imgRegex := regexp.MustCompile(`<img[^>]+src=["']([^"']+)["']`)
	matches := imgRegex.FindStringSubmatch(htmlContent)

	if len(matches) > 1 {
		return matches[1] // 返回第一个捕获组（图片URL）
	}

	// 2. 尝试在文本中直接寻找图片URL（.jpg, .png, .gif等格式）
	urlRegex := regexp.MustCompile(`https?://[^\s"']+\.(jpg|jpeg|png|gif|webp)`)
	urlMatches := urlRegex.FindString(htmlContent)

	if urlMatches != "" {
		return urlMatches
	}

	// 3. 检查Telegram CDN链接
	cdnRegex := regexp.MustCompile(`https?://cdn[0-9]*\.cdn-telegram\.org/[^\s"']+`)
	cdnMatches := cdnRegex.FindString(htmlContent)

	if cdnMatches != "" {
		return cdnMatches
	}

	// 没有找到图片，返回空字符串
	return ""
}

// cleanHTMLContent 清理HTML内容，移除Telegram不支持的标签
func cleanHTMLContent(htmlContent string) string {
	// 1. 移除img标签，但保留其它内容
	imgRegex := regexp.MustCompile(`<img[^>]*>`)
	content := imgRegex.ReplaceAllString(htmlContent, "")

	// 2. 替换<br>标签为换行符
	brRegex := regexp.MustCompile(`<br\s*\/?>`)
	content = brRegex.ReplaceAllString(content, "\n")

	// 3. 保留Telegram支持的标签，移除其他标签
	// Telegram支持的标签: <b>, <i>, <u>, <s>, <a>, <code>, <pre>
	// 我们采用分步骤处理的方式

	// 暂时标记支持的标签，以便后面恢复
	content = regexp.MustCompile(`<b>`).ReplaceAllString(content, "§§§B§§§")
	content = regexp.MustCompile(`</b>`).ReplaceAllString(content, "§§§/B§§§")
	content = regexp.MustCompile(`<i>`).ReplaceAllString(content, "§§§I§§§")
	content = regexp.MustCompile(`</i>`).ReplaceAllString(content, "§§§/I§§§")
	content = regexp.MustCompile(`<u>`).ReplaceAllString(content, "§§§U§§§")
	content = regexp.MustCompile(`</u>`).ReplaceAllString(content, "§§§/U§§§")
	content = regexp.MustCompile(`<s>`).ReplaceAllString(content, "§§§S§§§")
	content = regexp.MustCompile(`</s>`).ReplaceAllString(content, "§§§/S§§§")
	content = regexp.MustCompile(`<code>`).ReplaceAllString(content, "§§§CODE§§§")
	content = regexp.MustCompile(`</code>`).ReplaceAllString(content, "§§§/CODE§§§")
	content = regexp.MustCompile(`<pre>`).ReplaceAllString(content, "§§§PRE§§§")
	content = regexp.MustCompile(`</pre>`).ReplaceAllString(content, "§§§/PRE§§§")

	// 特殊处理a标签
	aTagRegex := regexp.MustCompile(`<a\s+href=["']([^"']+)["'][^>]*>`)
	content = aTagRegex.ReplaceAllString(content, "§§§A§§§$1§§§")
	content = regexp.MustCompile(`</a>`).ReplaceAllString(content, "§§§/A§§§")

	// 移除所有剩余的HTML标签
	allTagsRegex := regexp.MustCompile(`<[^>]*>`)
	content = allTagsRegex.ReplaceAllString(content, "")

	// 恢复支持的标签
	content = regexp.MustCompile(`§§§B§§§`).ReplaceAllString(content, "<b>")
	content = regexp.MustCompile(`§§§/B§§§`).ReplaceAllString(content, "</b>")
	content = regexp.MustCompile(`§§§I§§§`).ReplaceAllString(content, "<i>")
	content = regexp.MustCompile(`§§§/I§§§`).ReplaceAllString(content, "</i>")
	content = regexp.MustCompile(`§§§U§§§`).ReplaceAllString(content, "<u>")
	content = regexp.MustCompile(`§§§/U§§§`).ReplaceAllString(content, "</u>")
	content = regexp.MustCompile(`§§§S§§§`).ReplaceAllString(content, "<s>")
	content = regexp.MustCompile(`§§§/S§§§`).ReplaceAllString(content, "</s>")
	content = regexp.MustCompile(`§§§CODE§§§`).ReplaceAllString(content, "<code>")
	content = regexp.MustCompile(`§§§/CODE§§§`).ReplaceAllString(content, "</code>")
	content = regexp.MustCompile(`§§§PRE§§§`).ReplaceAllString(content, "<pre>")
	content = regexp.MustCompile(`§§§/PRE§§§`).ReplaceAllString(content, "</pre>")
	content = regexp.MustCompile(`§§§A§§§(.*?)§§§`).ReplaceAllString(content, `<a href="$1">`)
	content = regexp.MustCompile(`§§§/A§§§`).ReplaceAllString(content, "</a>")

	// 4. 移除连续的换行符
	multipleNewlinesRegex := regexp.MustCompile(`\n{3,}`)
	content = multipleNewlinesRegex.ReplaceAllString(content, "\n\n")

	return content
}
