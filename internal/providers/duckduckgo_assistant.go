package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// DuckDuckGoAssistantProvider is a built-in zero-key provider that answers
// user questions directly in natural Vietnamese by combining GoClaw's
// DuckDuckGo web_search tool with live data extractors (weather, gold prices,
// exchange rates, Wikipedia, and full-page article content synthesis).
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
		client:       &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *DuckDuckGoAssistantProvider) Name() string         { return p.name }
func (p *DuckDuckGoAssistantProvider) DefaultModel() string { return p.defaultModel }

func (p *DuckDuckGoAssistantProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	return p.ChatStream(ctx, req, nil)
}

func (p *DuckDuckGoAssistantProvider) ChatStream(ctx context.Context, req ChatRequest, onChunk func(StreamChunk)) (*ChatResponse, error) {
	// Skip background consolidation/KG internal prompts gracefully.
	if isInternalBackgroundPrompt(req.Messages) {
		return emitStreamResponse(`{"entities":[],"relations":[]}`, nil), nil
	}

	// 1. Check if the last message is a tool result from web_search.
	if len(req.Messages) > 0 {
		last := req.Messages[len(req.Messages)-1]
		if last.Role == "tool" {
			userQuery := extractLastUserQuery(req.Messages)
			reply := p.synthesizeDirectAnswer(ctx, userQuery, last.Content)
			return emitStreamResponse(reply, onChunk), nil
		}
	}

	userQuery := extractLastUserQuery(req.Messages)
	if userQuery == "" {
		reply := "👋 Xin chào @Hoangquy1104! Bạn hãy đặt câu hỏi bất kỳ nhé, mình sẽ trả lời chi tiết cho bạn."
		return emitStreamResponse(reply, onChunk), nil
	}

	if isGreetingOrStart(userQuery) {
		reply := "👋 Xin chào **@Hoangquy1104**!\n\n" +
			"Mình là trợ lý AI **GaAuto (@GaAuto_HQ_bot)**. Bạn có thể hỏi mình trực tiếp bất kỳ thông tin gì (ví dụ: *Thời tiết hôm nay ở Sầm Sơn*, *Giá vàng hôm nay*, *Tỷ giá USD*, hoặc bất kỳ câu hỏi kiến thức/tin tức nào) — mình sẽ tổng hợp và trả lời trực tiếp cho bạn!"
		return emitStreamResponse(reply, onChunk), nil
	}

	// 2. If web_search tool is available in req.Tools, invoke it via tool_calls first.
	for _, t := range req.Tools {
		if t.Function != nil && t.Function.Name == "web_search" {
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
	}

	// 3. Direct answer synthesis if web_search tool wasn't in req.Tools.
	reply := p.synthesizeDirectAnswer(ctx, userQuery, "")
	return emitStreamResponse(reply, onChunk), nil
}

func isInternalBackgroundPrompt(messages []Message) bool {
	if len(messages) == 0 {
		return false
	}
	for _, m := range messages {
		if strings.Contains(m.Content, "Extract entities and relations") ||
			strings.Contains(m.Content, "Summarize the conversation") {
			return true
		}
	}
	return false
}

// synthesizeDirectAnswer produces a natural Vietnamese answer instead of raw search results.
func (p *DuckDuckGoAssistantProvider) synthesizeDirectAnswer(ctx context.Context, query, toolOutput string) string {
	lower := strings.ToLower(query)

	// Case A: Weather questions ("thời tiết", "nhiệt độ", "mưa không", "dự báo thời tiết")
	if isWeatherQuery(lower) {
		if weatherAns := p.answerWeather(ctx, query); weatherAns != "" {
			return weatherAns
		}
	}

	// Case B: Gold price questions ("giá vàng", "vàng sjc", "vàng 9999", "vàng nhẫn")
	if isGoldPriceQuery(lower) {
		if goldAns := p.answerGoldPrice(ctx); goldAns != "" {
			return goldAns
		}
	}

	// Case C: Currency / Exchange rate questions ("tỷ giá", "giá đô", "giá usd", "ngoại tệ")
	if isCurrencyQuery(lower) {
		if fxAns := p.answerExchangeRate(ctx); fxAns != "" {
			return fxAns
		}
	}

	// Case D: General questions — synthesize from Wikipedia + article content + DuckDuckGo snippets
	return p.answerGeneralQuestion(ctx, query, toolOutput)
}

func isWeatherQuery(lower string) bool {
	keywords := []string{"thời tiết", "thoi tiet", "nhiệt độ", "nhiet do", "trời mưa", "có mưa không", "dự báo thời tiết", "nắng hay mưa"}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func isGoldPriceQuery(lower string) bool {
	keywords := []string{"giá vàng", "gia vang", "vàng sjc", "vang sjc", "vàng 9999", "vàng nhẫn", "vang nhan"}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func isCurrencyQuery(lower string) bool {
	keywords := []string{"tỷ giá", "ty gia", "giá usd", "gia usd", "giá đô", "gia do", "1 usd", "1 đô"}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// --- 1. Live Weather Synthesizer (Open-Meteo) ---

type openMeteoGeoResp struct {
	Results []struct {
		Name      string  `json:"name"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Admin1    string  `json:"admin1"`
		Country   string  `json:"country"`
	} `json:"results"`
}

type openMeteoForecastResp struct {
	Current struct {
		Temperature2m       float64 `json:"temperature_2m"`
		RelativeHumidity2m  int     `json:"relative_humidity_2m"`
		ApparentTemperature float64 `json:"apparent_temperature"`
		Precipitation       float64 `json:"precipitation"`
		WeatherCode         int     `json:"weather_code"`
		WindSpeed10m        float64 `json:"wind_speed_10m"`
	} `json:"current"`
	Daily struct {
		Time                        []string  `json:"time"`
		WeatherCode                 []int     `json:"weather_code"`
		Temperature2mMax            []float64 `json:"temperature_2m_max"`
		Temperature2mMin            []float64 `json:"temperature_2m_min"`
		PrecipitationProbabilityMax []int     `json:"precipitation_probability_max"`
	} `json:"daily"`
}

func (p *DuckDuckGoAssistantProvider) answerWeather(ctx context.Context, query string) string {
	loc := extractLocationFromWeatherQuery(query)
	if loc == "" {
		loc = "Hà Nội"
	}

	geoURL := fmt.Sprintf("https://geocoding-api.open-meteo.com/v1/search?name=%s&count=1&language=vi&format=json", url.QueryEscape(loc))
	req, err := http.NewRequestWithContext(ctx, "GET", geoURL, nil)
	if err != nil {
		return ""
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var geo openMeteoGeoResp
	if err := json.NewDecoder(resp.Body).Decode(&geo); err != nil || len(geo.Results) == 0 {
		// Fallback without diacritics if needed
		asciiLoc := removeVietnameseDiacritics(loc)
		geoURL2 := fmt.Sprintf("https://geocoding-api.open-meteo.com/v1/search?name=%s&count=1&language=vi&format=json", url.QueryEscape(asciiLoc))
		req2, err2 := http.NewRequestWithContext(ctx, "GET", geoURL2, nil)
		if err2 != nil {
			return ""
		}
		resp2, err2 := p.client.Do(req2)
		if err2 != nil {
			return ""
		}
		defer resp2.Body.Close()
		if err := json.NewDecoder(resp2.Body).Decode(&geo); err != nil || len(geo.Results) == 0 {
			return ""
		}
	}

	place := geo.Results[0]
	placeDisplay := place.Name
	if place.Admin1 != "" && !strings.EqualFold(place.Admin1, place.Name) {
		placeDisplay = fmt.Sprintf("%s, %s", place.Name, place.Admin1)
	}

	fcURL := fmt.Sprintf(
		"https://api.open-meteo.com/v1/forecast?latitude=%.4f&longitude=%.4f&current=temperature_2m,relative_humidity_2m,apparent_temperature,precipitation,weather_code,wind_speed_10m&daily=weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max&timezone=Asia%%2FHo_Chi_Minh&forecast_days=2",
		place.Latitude, place.Longitude,
	)
	fcReq, err := http.NewRequestWithContext(ctx, "GET", fcURL, nil)
	if err != nil {
		return ""
	}
	fcResp, err := p.client.Do(fcReq)
	if err != nil {
		return ""
	}
	defer fcResp.Body.Close()

	var fc openMeteoForecastResp
	if err := json.NewDecoder(fcResp.Body).Decode(&fc); err != nil {
		return ""
	}

	curDesc := weatherCodeToVietnamese(fc.Current.WeatherCode)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🌤️ **Thời tiết tại %s hiện tại:**\n\n", placeDisplay))
	sb.WriteString(fmt.Sprintf("• **Tình trạng:** %s\n", curDesc))
	sb.WriteString(fmt.Sprintf("• **Nhiệt độ hiện tại:** **%.1f°C** (cảm giác thực tế như **%.1f°C**)\n", fc.Current.Temperature2m, fc.Current.ApparentTemperature))
	sb.WriteString(fmt.Sprintf("• **Độ ẩm:** %d%%\n", fc.Current.RelativeHumidity2m))
	sb.WriteString(fmt.Sprintf("• **Tốc độ gió:** %.1f km/h\n", fc.Current.WindSpeed10m))

	if len(fc.Daily.Time) > 0 {
		todayDesc := weatherCodeToVietnamese(fc.Daily.WeatherCode[0])
		rainProb := 0
		if len(fc.Daily.PrecipitationProbabilityMax) > 0 {
			rainProb = fc.Daily.PrecipitationProbabilityMax[0]
		}
		sb.WriteString(fmt.Sprintf("\n📅 **Tổng quan hôm nay (%s):**\n", fc.Daily.Time[0]))
		sb.WriteString(fmt.Sprintf("• Nhiệt độ trong ngày: **%.1f°C – %.1f°C**\n", fc.Daily.Temperature2mMin[0], fc.Daily.Temperature2mMax[0]))
		sb.WriteString(fmt.Sprintf("• Dự báo: %s (Khả năng mưa: **%d%%**)\n", todayDesc, rainProb))
	}

	if len(fc.Daily.Time) > 1 {
		tmrDesc := weatherCodeToVietnamese(fc.Daily.WeatherCode[1])
		tmrRain := 0
		if len(fc.Daily.PrecipitationProbabilityMax) > 1 {
			tmrRain = fc.Daily.PrecipitationProbabilityMax[1]
		}
		sb.WriteString(fmt.Sprintf("\n📆 **Dự báo ngày mai (%s):**\n", fc.Daily.Time[1]))
		sb.WriteString(fmt.Sprintf("• Nhiệt độ: **%.1f°C – %.1f°C** — %s (Khả năng mưa: **%d%%**)", fc.Daily.Temperature2mMin[1], fc.Daily.Temperature2mMax[1], tmrDesc, tmrRain))
	}

	return sb.String()
}

func extractLocationFromWeatherQuery(q string) string {
	cleaned := strings.ToLower(q)
	removePhrases := []string{
		"dự báo thời tiết", "thời tiết hôm nay", "thời tiết ngày mai", "thời tiết hiện tại", "thời tiết",
		"nhiệt độ hôm nay", "nhiệt độ", "hôm nay", "ngày mai", "bây giờ", "hiện tại",
		"như thế nào", "thế nào", "ra sao", "có mưa không", "nắng hay mưa",
		"ở tại", "tại", "khu vực", "thành phố", "tỉnh", "biển", "ở", "?", ".", ",", "!",
	}
	for _, p := range removePhrases {
		cleaned = strings.ReplaceAll(cleaned, p, " ")
	}
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	if cleaned == "" {
		return "Hà Nội"
	}
	return cleaned
}

func weatherCodeToVietnamese(code int) string {
	switch code {
	case 0:
		return "Trời quang đãng, không mưa ☀️"
	case 1, 2:
		return "Trời ít mây, nắng nhẹ 🌤️"
	case 3:
		return "Trời nhiều mây ☁️"
	case 45, 48:
		return "Có sương mù 🌫️"
	case 51, 53, 55:
		return "Mưa phùn nhẹ 🌦️"
	case 61, 63:
		return "Có mưa rào nhẹ đến vừa 🌧️"
	case 65:
		return "Mưa to 🌧️"
	case 80, 81, 82:
		return "Mưa rào rải rác 🌦️"
	case 95, 96, 99:
		return "Có mưa dông ⛈️"
	default:
		return "Trời có mây ⛅"
	}
}

// --- 2. Live Gold Price Synthesizer ---

var trRowRe = regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr>`)
var tdCellRe = regexp.MustCompile(`(?is)<t[dh][^>]*>(.*?)</t[dh]>`)

func (p *DuckDuckGoAssistantProvider) answerGoldPrice(ctx context.Context) string {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://webgia.com/gia-vang/sjc/", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := p.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	rows := trRowRe.FindAllStringSubmatch(string(body), 15)
	type goldItem struct {
		name string
		buy  string
		sell string
	}
	var items []goldItem

	for _, r := range rows {
		cells := tdCellRe.FindAllStringSubmatch(r[1], -1)
		if len(cells) < 3 {
			continue
		}
		var vals []string
		for _, c := range cells {
			txt := strings.TrimSpace(ddgProvTagRe.ReplaceAllString(c[1], " "))
			txt = strings.Join(strings.Fields(html.UnescapeString(txt)), " ")
			vals = append(vals, txt)
		}
		// Match rows that have numeric prices like 14.000.000
		if len(vals) >= 4 && strings.Contains(vals[len(vals)-2], ".") && strings.Contains(vals[len(vals)-1], ".") {
			name := vals[len(vals)-3]
			buy := vals[len(vals)-2]
			sell := vals[len(vals)-1]
			if !strings.Contains(strings.ToLower(buy), "web") {
				items = append(items, goldItem{name: name, buy: buy, sell: sell})
			}
		} else if len(vals) == 3 && strings.Contains(vals[1], ".") && strings.Contains(vals[2], ".") {
			if !strings.Contains(strings.ToLower(vals[1]), "web") {
				items = append(items, goldItem{name: vals[0], buy: vals[1], sell: vals[2]})
			}
		}
		if len(items) >= 5 {
			break
		}
	}

	if len(items) == 0 {
		return ""
	}

	nowStr := time.Now().Format("15:04 ngày 02/01/2006")
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("💰 **Bảng giá vàng SJC & Vàng nhẫn 99,99%% hôm nay** *(cập nhật lúc %s)*:\n\n", nowStr))
	for _, it := range items {
		sb.WriteString(fmt.Sprintf("• **%s**:\n  - Mua vào: **%s đ/chỉ**\n  - Bán ra: **%s đ/chỉ**\n", it.name, it.buy, it.sell))
	}
	sb.WriteString("\n*(Lưu ý: 1 lượng = 10 chỉ. Giá vàng có thể chênh lệch nhẹ tùy từng cửa hàng và khu vực).*")
	return sb.String()
}

// --- 3. Live Exchange Rate Synthesizer ---

func (p *DuckDuckGoAssistantProvider) answerExchangeRate(ctx context.Context) string {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://open.er-api.com/v6/latest/USD", nil)
	if err != nil {
		return ""
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var data struct {
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return ""
	}
	vnd, ok := data.Rates["VND"]
	if !ok || vnd <= 0 {
		return ""
	}

	return fmt.Sprintf(
		"💵 **Tỷ giá ngoại tệ hôm nay (%s):**\n\n"+
			"• **1 USD (Đô la Mỹ)** ≈ **%.0f VNĐ**\n"+
			"• **100 USD** ≈ **%.0f VNĐ**\n",
		time.Now().Format("02/01/2006"), vnd, vnd*100,
	)
}

// --- 4. General Question Synthesizer (Wikipedia + Article Content + Search Snippets) ---

type parsedSearchItem struct {
	Title   string
	URL     string
	Snippet string
}

var paragraphRe = regexp.MustCompile(`(?is)<p[^>]*>(.*?)</p>`)
var scriptStyleRe = regexp.MustCompile(`(?is)<(script|style|nav|header|footer|noscript)[^>]*>.*?</(script|style|nav|header|footer|noscript)>`)

func (p *DuckDuckGoAssistantProvider) answerGeneralQuestion(ctx context.Context, query, toolOutput string) string {
	// Parse structured search items from toolOutput (or run direct search if empty)
	items := parseSearchItemsFromToolOutput(toolOutput)
	if len(items) == 0 {
		items = p.fetchDDGItems(ctx, query, 5)
	}

	// 1. Try Vietnamese Wikipedia summary if applicable
	wikiExtract := p.fetchVietnameseWikipediaSummary(ctx, query)

	// 2. Try extracting clean article paragraphs from the top search result URL
	var articleParagraphs []string
	for i := 0; i < len(items) && i < 2; i++ {
		paras := p.fetchReadableParagraphs(ctx, items[i].URL)
		if len(paras) > 0 {
			articleParagraphs = paras
			break
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("💡 **Câu trả lời cho:** *%s*\n\n", query))

	wroteContent := false
	if wikiExtract != "" {
		sb.WriteString(wikiExtract + "\n\n")
		wroteContent = true
	}

	if len(articleParagraphs) > 0 {
		maxP := 3
		if wroteContent {
			maxP = 2
		}
		for i := 0; i < len(articleParagraphs) && i < maxP; i++ {
			sb.WriteString(articleParagraphs[i] + "\n\n")
		}
		wroteContent = true
	}

	// Combine snippets into coherent bullet points if we need additional context
	if !wroteContent && len(items) > 0 {
		for i := 0; i < len(items) && i < 3; i++ {
			if items[i].Snippet != "" {
				sb.WriteString(fmt.Sprintf("• %s\n", items[i].Snippet))
				wroteContent = true
			}
		}
		sb.WriteByte('\n')
	}

	if !wroteContent {
		return fmt.Sprintf("Mình đã tra cứu thông tin về **%s** nhưng hiện chưa có dữ liệu chi tiết. Bạn thử diễn đạt câu hỏi cụ thể hơn nhé!", query)
	}

	// Add 1-2 clean reference links at the bottom
	if len(items) > 0 {
		sb.WriteString("📌 **Nguồn tham khảo:**\n")
		limit := 2
		if len(items) < limit {
			limit = len(items)
		}
		for i := 0; i < limit; i++ {
			sb.WriteString(fmt.Sprintf("• [%s](%s)\n", items[i].Title, items[i].URL))
		}
	}

	return strings.TrimSpace(sb.String())
}

func parseSearchItemsFromToolOutput(toolOutput string) []parsedSearchItem {
	cleaned := ddgWrapTagRe.ReplaceAllString(toolOutput, "")
	lines := strings.Split(cleaned, "\n")
	var items []parsedSearchItem
	var cur *parsedSearchItem

	numPrefixRe := regexp.MustCompile(`^\d+\.\s+(.+)$`)
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "Source:") || strings.HasPrefix(line, "---") || strings.HasPrefix(line, "Search results for:") || strings.HasPrefix(line, "<<<") {
			continue
		}
		if m := numPrefixRe.FindStringSubmatch(line); len(m) == 2 {
			if cur != nil && cur.Title != "" {
				items = append(items, *cur)
			}
			cur = &parsedSearchItem{Title: strings.TrimSpace(m[1])}
			continue
		}
		if cur != nil {
			if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
				cur.URL = line
			} else if cur.Snippet == "" {
				cur.Snippet = html.UnescapeString(line)
			} else {
				cur.Snippet += " " + html.UnescapeString(line)
			}
		}
	}
	if cur != nil && cur.Title != "" {
		items = append(items, *cur)
	}
	return items
}

func (p *DuckDuckGoAssistantProvider) fetchVietnameseWikipediaSummary(ctx context.Context, query string) string {
	apiURL := fmt.Sprintf(
		"https://vi.wikipedia.org/w/api.php?action=query&list=search&srsearch=%s&utf8=1&format=json&srlimit=1",
		url.QueryEscape(query),
	)
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "GoClawAssistant/1.0")
	resp, err := p.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var searchData struct {
		Query struct {
			Search []struct {
				Title string `json:"title"`
			} `json:"search"`
		} `json:"query"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&searchData); err != nil || len(searchData.Query.Search) == 0 {
		return ""
	}

	title := searchData.Query.Search[0].Title
	sumURL := fmt.Sprintf("https://vi.wikipedia.org/api/rest_v1/page/summary/%s", url.PathEscape(title))
	sumReq, err := http.NewRequestWithContext(ctx, "GET", sumURL, nil)
	if err != nil {
		return ""
	}
	sumReq.Header.Set("User-Agent", "GoClawAssistant/1.0")
	sumResp, err := p.client.Do(sumReq)
	if err != nil {
		return ""
	}
	defer sumResp.Body.Close()

	var sumData struct {
		Extract string `json:"extract"`
	}
	if err := json.NewDecoder(sumResp.Body).Decode(&sumData); err != nil {
		return ""
	}
	extract := strings.TrimSpace(sumData.Extract)
	if len(extract) < 40 {
		return ""
	}
	return extract
}

func (p *DuckDuckGoAssistantProvider) fetchReadableParagraphs(ctx context.Context, pageURL string) []string {
	if pageURL == "" {
		return nil
	}
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "GET", pageURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 250*1024))
	if err != nil {
		return nil
	}

	cleanedHTML := scriptStyleRe.ReplaceAllString(string(body), "")
	matches := paragraphRe.FindAllStringSubmatch(cleanedHTML, 30)
	var paras []string
	for _, m := range matches {
		txt := strings.TrimSpace(ddgProvTagRe.ReplaceAllString(m[1], " "))
		txt = strings.Join(strings.Fields(html.UnescapeString(txt)), " ")
		// Keep meaningful paragraphs (at least 80 chars, not cookie/copyright notices)
		if len(txt) >= 80 && !strings.Contains(strings.ToLower(txt), "cookie") && !strings.Contains(strings.ToLower(txt), "bản quyền") {
			paras = append(paras, txt)
			if len(paras) >= 3 {
				break
			}
		}
	}
	return paras
}

func (p *DuckDuckGoAssistantProvider) fetchDDGItems(ctx context.Context, query string, count int) []parsedSearchItem {
	searchURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query))
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	htmlStr := string(body)
	linkMatches := ddgProvLinkRe.FindAllStringSubmatch(htmlStr, count+3)
	snippetMatches := ddgProvSnippetRe.FindAllStringSubmatch(htmlStr, count+3)

	var items []parsedSearchItem
	for i := 0; i < len(linkMatches) && i < count; i++ {
		rawURL := linkMatches[i][1]
		title := html.UnescapeString(strings.TrimSpace(ddgProvTagRe.ReplaceAllString(linkMatches[i][2], "")))
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
			desc = html.UnescapeString(strings.TrimSpace(ddgProvTagRe.ReplaceAllString(snippetMatches[i][1], "")))
		}
		items = append(items, parsedSearchItem{
			Title:   title,
			URL:     rawURL,
			Snippet: desc,
		})
	}
	return items
}

func removeVietnameseDiacritics(s string) string {
	replacer := strings.NewReplacer(
		"à", "a", "á", "a", "ạ", "a", "ả", "a", "ã", "a",
		"â", "a", "ầ", "a", "ấ", "a", "ậ", "a", "ẩ", "a", "ẫ", "a",
		"ă", "a", "ằ", "a", "ắ", "a", "ặ", "a", "ẳ", "a", "ẵ", "a",
		"è", "e", "é", "e", "ẹ", "e", "ẻ", "e", "ẽ", "e",
		"ê", "e", "ề", "e", "ế", "e", "ệ", "e", "ể", "e", "ễ", "e",
		"ì", "i", "í", "i", "ị", "i", "ỉ", "i", "ĩ", "i",
		"ò", "o", "ó", "o", "ọ", "o", "ỏ", "o", "õ", "o",
		"ô", "o", "ồ", "o", "ố", "o", "ộ", "o", "ổ", "o", "ỗ", "o",
		"ơ", "o", "ờ", "o", "ớ", "o", "ợ", "o", "ở", "o", "ỡ", "o",
		"ù", "u", "ú", "u", "ụ", "u", "ủ", "u", "ũ", "u",
		"ư", "u", "ừ", "u", "ứ", "u", "ự", "u", "ử", "u", "ữ", "u",
		"ỳ", "y", "ý", "y", "ỵ", "y", "ỷ", "y", "ỹ", "y",
		"đ", "d", "Đ", "D",
	)
	return replacer.Replace(s)
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
	ddgWrapTagRe     = regexp.MustCompile(`(?s)<<+<[^>]*>+|Source:\s*Web Search|---`)
)
