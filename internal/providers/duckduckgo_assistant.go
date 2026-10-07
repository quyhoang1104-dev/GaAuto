package providers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// DuckDuckGoAssistantProvider is a built-in zero-key provider that answers
// greetings/status checks directly and uses GoClaw's web_search tool (or direct
// DuckDuckGo HTML search fallback) to answer any user query in Vietnamese.
type DuckDuckGoAssistantProvider struct {
	name         string
	defaultModel string
	client       *http.Client
}

// NewDuckDuckGoAssistantProvider creates a new built-in DuckDuckGo assistant provider.
func NewDuckDuckGoAssistantProvider() *DuckDuckGoAssistantProvider {
	return &DuckDuckGoAssistantProvider{
		name:         "duckduckgo",
		defaultModel: "duckduckgo-search",
		client:       &http.Client{Timeout: 20 * time.Second},
	}
}

func (p *DuckDuckGoAssistantProvider) Name() string         { return p.name }
func (p *DuckDuckGoAssistantProvider) DefaultModel() string { return p.defaultModel }

func (p *DuckDuckGoAssistantProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	return p.ChatStream(ctx, req, nil)
}

func (p *DuckDuckGoAssistantProvider) ChatStream(ctx context.Context, req ChatRequest, onChunk func(StreamChunk)) (*ChatResponse, error) {
	// 1. Check if the last message is a tool result from web_search.
	if len(req.Messages) > 0 {
		last := req.Messages[len(req.Messages)-1]
		if last.Role == "tool" {
			userQuery := extractLastUserQuery(req.Messages)
			reply := formatToolSearchOutput(userQuery, last.Content)
			return emitStreamResponse(reply, onChunk), nil
		}
	}

	userQuery := extractLastUserQuery(req.Messages)
	if userQuery == "" {
		reply := "👋 Xin chào @Hoangquy1104! Mình là GaAuto Bot. Bạn cần mình tìm kiếm thông tin gì trên DuckDuckGo hôm nay?"
		return emitStreamResponse(reply, onChunk), nil
	}

	if isGreetingOrStart(userQuery) {
		reply := "👋 Xin chào **@Hoangquy1104**!\n\n" +
			"🤖 **GaAuto Bot (@GaAuto_HQ_bot)** đã kết nối thành công với **GoClaw** ở chế độ tự động:\n" +
			"• 🔒 **Chế độ bảo mật (dmPolicy)**: `allowlist` (cấp quyền duy nhất cho `@Hoangquy1104`)\n" +
			"• 🔍 **Tìm kiếm DuckDuckGo**: Đã bật sẵn sàng (`web_search`)\n\n" +
			"👉 Bạn hãy nhắn bất kỳ câu hỏi hoặc từ khóa nào (ví dụ: *Giá vàng hôm nay*, *Thời tiết Hà Nội*, *Tin tức AI mới nhất*...), mình sẽ tra cứu trực tiếp trên **DuckDuckGo** và phản hồi ngay!"
		return emitStreamResponse(reply, onChunk), nil
	}

	// 2. If web_search tool is available in req.Tools, invoke it via tool_calls so the full GoClaw pipeline runs.
	hasWebSearchTool := false
	for _, t := range req.Tools {
		if t.Function != nil && t.Function.Name == "web_search" {
			hasWebSearchTool = true
			break
		}
	}

	if hasWebSearchTool {
		return &ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls: []ToolCall{
				{
					ID:   fmt.Sprintf("call_ddg_%d", time.Now().UnixNano()),
					Name: "web_search",
					Arguments: map[string]any{
						"query": userQuery,
						"count": float64(5),
					},
				},
			},
			Usage: &Usage{PromptTokens: 20, CompletionTokens: 15, TotalTokens: 35, RequestCount: 1},
		}, nil
	}

	// 3. Direct DuckDuckGo fallback if web_search tool was not in req.Tools.
	rawResults, err := p.directDDGSearch(ctx, userQuery, 5)
	if err != nil || rawResults == "" {
		reply := fmt.Sprintf("🔍 Mình đã nhận câu hỏi của bạn: **%s**\nHiện tại chưa tìm thấy kết quả phù hợp trên DuckDuckGo, bạn thử từ khóa cụ thể hơn nhé!", userQuery)
		return emitStreamResponse(reply, onChunk), nil
	}
	return emitStreamResponse(rawResults, onChunk), nil
}

func emitStreamResponse(content string, onChunk func(StreamChunk)) *ChatResponse {
	if onChunk != nil {
		onChunk(StreamChunk{Content: content})
		onChunk(StreamChunk{Done: true})
	}
	return &ChatResponse{
		Content:      content,
		FinishReason: "stop",
		Usage:        &Usage{PromptTokens: 30, CompletionTokens: len(content) / 4, TotalTokens: 30 + len(content)/4, RequestCount: 1},
	}
}

func extractLastUserQuery(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			text := strings.TrimSpace(messages[i].Content)
			// Strip Telegram sender header "[From: @username (...)]\n"
			if strings.HasPrefix(text, "[From:") {
				if idx := strings.IndexByte(text, '\n'); idx != -1 {
					text = strings.TrimSpace(text[idx+1:])
				}
			}
			return text
		}
	}
	return ""
}

func isGreetingOrStart(q string) bool {
	lower := strings.ToLower(strings.TrimSpace(q))
	switch lower {
	case "/start", "start", "hi", "hello", "xin chào", "xin chao", "chào", "chao", "alo", "test", "ping", "bot", "bot ơi", "bot oi":
		return true
	}
	return false
}

var (
	ddgProvLinkRe    = regexp.MustCompile(`<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>([\s\S]*?)</a>`)
	ddgProvSnippetRe = regexp.MustCompile(`<a class="result__snippet[^"]*".*?>([\s\S]*?)</a>`)
	ddgProvTagRe     = regexp.MustCompile(`<[^>]+>`)
	ddgWrapTagRe     = regexp.MustCompile(`(?s)</?external_content[^>]*>`)
)

func formatToolSearchOutput(query, toolContent string) string {
	cleaned := strings.TrimSpace(ddgWrapTagRe.ReplaceAllString(toolContent, ""))
	if cleaned == "" || strings.HasPrefix(cleaned, "No results found") {
		return fmt.Sprintf("🔍 Không tìm thấy kết quả nào trên DuckDuckGo cho từ khóa: **%s**.\nBạn hãy thử lại với từ khóa chi tiết hơn nhé!", query)
	}
	return fmt.Sprintf("🔍 **Kết quả tìm kiếm DuckDuckGo cho:** `%s`\n\n%s", query, cleaned)
}

func (p *DuckDuckGoAssistantProvider) directDDGSearch(ctx context.Context, query string, count int) (string, error) {
	searchURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query))
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	html := string(body)
	linkMatches := ddgProvLinkRe.FindAllStringSubmatch(html, count+3)
	if len(linkMatches) == 0 {
		return "", nil
	}
	snippetMatches := ddgProvSnippetRe.FindAllStringSubmatch(html, count+3)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔍 **Kết quả tìm kiếm DuckDuckGo cho:** `%s`\n\n", query))
	for i := 0; i < len(linkMatches) && i < count; i++ {
		rawURL := linkMatches[i][1]
		title := strings.TrimSpace(ddgProvTagRe.ReplaceAllString(linkMatches[i][2], ""))
		if strings.Contains(rawURL, "uddg=") {
			if u, err := url.QueryUnescape(rawURL); err == nil {
				if _, after, ok := strings.Cut(u, "uddg="); ok {
					if ampIdx := strings.Index(after, "&"); ampIdx != -1 {
						after = after[:ampIdx]
					}
					rawURL = after
				}
			}
		}
		desc := ""
		if i < len(snippetMatches) {
			desc = strings.TrimSpace(ddgProvTagRe.ReplaceAllString(snippetMatches[i][1], ""))
		}
		sb.WriteString(fmt.Sprintf("%d. **%s**\n   🔗 %s\n", i+1, title, rawURL))
		if desc != "" {
			sb.WriteString(fmt.Sprintf("   📝 %s\n", desc))
		}
		sb.WriteByte('\n')
	}
	return sb.String(), nil
}
