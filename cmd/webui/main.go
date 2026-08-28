package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/baseredge/llmdock/dto"
	"github.com/baseredge/llmdock/relayconvert"
	"github.com/baseredge/llmdock/relayconvert/convmeta"
	"github.com/baseredge/llmdock/types"
)

//go:embed index.html
var htmlIndex string

//go:embed app.js
var jsApp string

// ModelConfig 单个模型的详细配置
type ModelConfig struct {
	ID              string `json:"id"`                // 上游实际模型名 (如 claude-3-7-sonnet-20250219)
	Alias           string `json:"alias"`             // 对外暴露的别名 (如 claude-3-7-sonnet 或 gpt-4o)
	Enabled         bool   `json:"enabled"`           // 是否勾选启用
	ContextTokens   int    `json:"context_tokens"`    // 上下文容量 (如 200000, 128000)
	MaxOutputTokens int    `json:"max_output_tokens"` // 最大输出 Tokens (如 8192, 4096, 64000)
	ThinkingMode    string `json:"thinking_mode"`     // 思考强度: "off", "low", "medium", "high", "budget"
	ThinkingBudget  int    `json:"thinking_budget"`   // 思考 Token 预算 (如 4096, 8192, 16000)
}

// ChannelConfig 单个渠道配置
type ChannelConfig struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`     // 渠道备注名 (如 "Anthropic Claude 官方主渠道")
	Provider string        `json:"provider"` // "claude", "openai", "gemini", "deepseek", "ollama"
	APIKey   string        `json:"api_key"`
	BaseURL  string        `json:"base_url"`
	Models   []ModelConfig `json:"models"`   // 该渠道下的多个模型配置列表
}

type AppConfig struct {
	mu       sync.RWMutex
	Channels []ChannelConfig `json:"channels"`
}

func defaultModelsForProvider(provider string) []ModelConfig {
	switch strings.ToLower(provider) {
	case "claude":
		return []ModelConfig{
			{ID: "claude-3-7-sonnet-20250219", Alias: "claude-3-7-sonnet", Enabled: true, ContextTokens: 200000, MaxOutputTokens: 65536, ThinkingMode: "medium", ThinkingBudget: 4096},
			{ID: "claude-3-5-sonnet-20241022", Alias: "claude-3-5-sonnet", Enabled: true, ContextTokens: 200000, MaxOutputTokens: 65536, ThinkingMode: "off", ThinkingBudget: 0},
			{ID: "claude-3-5-haiku-20241022", Alias: "claude-3-5-haiku", Enabled: true, ContextTokens: 200000, MaxOutputTokens: 16384, ThinkingMode: "off", ThinkingBudget: 0},
			{ID: "claude-3-opus-20240229", Alias: "claude-3-opus", Enabled: false, ContextTokens: 200000, MaxOutputTokens: 8192, ThinkingMode: "off", ThinkingBudget: 0},
		}
	case "deepseek":
		return []ModelConfig{
			{ID: "deepseek-chat", Alias: "deepseek-chat", Enabled: true, ContextTokens: 256000, MaxOutputTokens: 16384, ThinkingMode: "off", ThinkingBudget: 0},
			{ID: "deepseek-reasoner", Alias: "deepseek-reasoner", Enabled: true, ContextTokens: 256000, MaxOutputTokens: 65536, ThinkingMode: "high", ThinkingBudget: 8192},
		}
	case "gemini":
		return []ModelConfig{
			{ID: "gemini-2.0-flash", Alias: "gemini-2.0-flash", Enabled: true, ContextTokens: 1000000, MaxOutputTokens: 65536, ThinkingMode: "off", ThinkingBudget: 0},
			{ID: "gemini-1.5-pro", Alias: "gemini-1.5-pro", Enabled: true, ContextTokens: 1000000, MaxOutputTokens: 65536, ThinkingMode: "off", ThinkingBudget: 0},
		}
	default:
		return []ModelConfig{
			{ID: "gpt-4o", Alias: "gpt-4o", Enabled: true, ContextTokens: 128000, MaxOutputTokens: 16384, ThinkingMode: "off", ThinkingBudget: 0},
			{ID: "gpt-4o-mini", Alias: "gpt-4o-mini", Enabled: true, ContextTokens: 128000, MaxOutputTokens: 16384, ThinkingMode: "off", ThinkingBudget: 0},
			{ID: "o3-mini", Alias: "o3-mini", Enabled: true, ContextTokens: 200000, MaxOutputTokens: 65536, ThinkingMode: "medium", ThinkingBudget: 4096},
			{ID: "o1", Alias: "o1", Enabled: false, ContextTokens: 200000, MaxOutputTokens: 65536, ThinkingMode: "high", ThinkingBudget: 16000},
		}
	}
}

var globalConfig = &AppConfig{
	Channels: []ChannelConfig{
		{
			ID:       "ch_claude",
			Name:     "Anthropic 官方主渠道",
			Provider: "claude",
			APIKey:   "",
			BaseURL:  "https://api.anthropic.com",
			Models:   defaultModelsForProvider("claude"),
		},
		{
			ID:       "ch_deepseek",
			Name:     "DeepSeek 官方渠道",
			Provider: "deepseek",
			APIKey:   "",
			BaseURL:  "https://api.deepseek.com",
			Models:   defaultModelsForProvider("deepseek"),
		},
		{
			ID:       "ch_openai",
			Name:     "OpenAI 官方渠道",
			Provider: "openai",
			APIKey:   "",
			BaseURL:  "https://api.openai.com",
			Models:   defaultModelsForProvider("openai"),
		},
	},
}

const configFilePath = "config.json"

func loadConfig() {
	data, err := os.ReadFile(configFilePath)
	if err == nil {
		globalConfig.mu.Lock()
		defer globalConfig.mu.Unlock()
		json.Unmarshal(data, globalConfig)
	}
}

func saveConfig() error {
	globalConfig.mu.RLock()
	data, err := json.MarshalIndent(globalConfig, "", "  ")
	globalConfig.mu.RUnlock()
	if err != nil {
		return err
	}
	return os.WriteFile(configFilePath, data, 0644)
}

func findChannelAndModel(modelName string) (*ChannelConfig, *ModelConfig) {
	globalConfig.mu.RLock()
	defer globalConfig.mu.RUnlock()

	// 1. 精确匹配 Alias 或 ID (且 Enabled)
	for _, ch := range globalConfig.Channels {
		for _, m := range ch.Models {
			if m.Enabled && (strings.EqualFold(m.Alias, modelName) || strings.EqualFold(m.ID, modelName)) {
				copyCh := ch
				copyM := m
				return &copyCh, &copyM
			}
		}
	}
	// 2. 匹配任何已启用的模型
	for _, ch := range globalConfig.Channels {
		for _, m := range ch.Models {
			if strings.EqualFold(m.Alias, modelName) || strings.EqualFold(m.ID, modelName) {
				copyCh := ch
				copyM := m
				return &copyCh, &copyM
			}
		}
	}
	// 3. 模糊前缀匹配
	for _, ch := range globalConfig.Channels {
		if strings.Contains(strings.ToLower(modelName), strings.ToLower(ch.Provider)) {
			copyCh := ch
			if len(ch.Models) > 0 {
				copyM := ch.Models[0]
				return &copyCh, &copyM
			}
			return &copyCh, &ModelConfig{ID: modelName, Alias: modelName, MaxOutputTokens: 4096}
		}
	}
	if len(globalConfig.Channels) > 0 {
		copyCh := globalConfig.Channels[0]
		if len(copyCh.Models) > 0 {
			copyM := copyCh.Models[0]
			return &copyCh, &copyM
		}
		return &copyCh, &ModelConfig{ID: modelName, Alias: modelName, MaxOutputTokens: 4096}
	}
	return nil, nil
}

type FetchModelsReq struct {
	Provider string `json:"provider"`
	APIKey   string `json:"api_key"`
	BaseURL  string `json:"base_url"`
}

func handleFetchModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	var req FetchModelsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "请求参数非法"})
		return
	}

	provider := strings.ToLower(req.Provider)
	baseURL := strings.TrimRight(req.BaseURL, "/")
	client := &http.Client{Timeout: 15 * time.Second}

	var fetchedIDs []string

	switch provider {
	case "claude":
		if baseURL == "" {
			baseURL = "https://api.anthropic.com"
		}
		httpReq, _ := http.NewRequest(http.MethodGet, baseURL+"/v1/models", nil)
		httpReq.Header.Set("x-api-key", req.APIKey)
		httpReq.Header.Set("anthropic-version", "2023-06-01")
		resp, err := client.Do(httpReq)
		if err == nil && resp.StatusCode == http.StatusOK {
			var res struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			json.NewDecoder(resp.Body).Decode(&res)
			resp.Body.Close()
			for _, d := range res.Data {
				fetchedIDs = append(fetchedIDs, d.ID)
			}
		}
		if len(fetchedIDs) == 0 {
			for _, m := range defaultModelsForProvider("claude") {
				fetchedIDs = append(fetchedIDs, m.ID)
			}
		}

	case "gemini":
		if baseURL == "" {
			baseURL = "https://generativelanguage.googleapis.com"
		}
		url := fmt.Sprintf("%s/v1beta/models?key=%s", baseURL, req.APIKey)
		resp, err := client.Get(url)
		if err == nil && resp.StatusCode == http.StatusOK {
			var res struct {
				Models []struct {
					Name string `json:"name"`
				} `json:"models"`
			}
			json.NewDecoder(resp.Body).Decode(&res)
			resp.Body.Close()
			for _, m := range res.Models {
				id := strings.TrimPrefix(m.Name, "models/")
				if strings.HasPrefix(id, "gemini") {
					fetchedIDs = append(fetchedIDs, id)
				}
			}
		}
		if len(fetchedIDs) == 0 {
			for _, m := range defaultModelsForProvider("gemini") {
				fetchedIDs = append(fetchedIDs, m.ID)
			}
		}

	case "ollama":
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		resp, err := client.Get(baseURL + "/api/tags")
		if err == nil && resp.StatusCode == http.StatusOK {
			var res struct {
				Models []struct {
					Name string `json:"name"`
				} `json:"models"`
			}
			json.NewDecoder(resp.Body).Decode(&res)
			resp.Body.Close()
			for _, m := range res.Models {
				fetchedIDs = append(fetchedIDs, m.Name)
			}
		}
		if len(fetchedIDs) == 0 {
			fetchedIDs = []string{"llama3:latest", "qwen2.5:latest", "deepseek-r1:latest"}
		}

	default: // openai, deepseek
		if baseURL == "" {
			if provider == "deepseek" {
				baseURL = "https://api.deepseek.com"
			} else {
				baseURL = "https://api.openai.com"
			}
		}
		httpReq, _ := http.NewRequest(http.MethodGet, baseURL+"/v1/models", nil)
		if req.APIKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)
		}
		resp, err := client.Do(httpReq)
		if err == nil && resp.StatusCode == http.StatusOK {
			var res struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			json.NewDecoder(resp.Body).Decode(&res)
			resp.Body.Close()
			for _, d := range res.Data {
				fetchedIDs = append(fetchedIDs, d.ID)
			}
		}
		if len(fetchedIDs) == 0 {
			for _, m := range defaultModelsForProvider(provider) {
				fetchedIDs = append(fetchedIDs, m.ID)
			}
		}
	}

	// 将拉取到的模型转换为完整的 ModelConfig 列表
	var models []ModelConfig
	for _, id := range fetchedIDs {
		ctx := 128000
		maxOut := 4096
		thinking := "off"
		budget := 0

		idLower := strings.ToLower(id)
		if strings.Contains(idLower, "claude-3-7") {
			ctx = 200000
			maxOut = 65536
			thinking = "medium"
			budget = 4096
		} else if strings.Contains(idLower, "claude") {
			ctx = 200000
			maxOut = 65536
		} else if strings.Contains(idLower, "gemini") {
			ctx = 1000000
			maxOut = 65536
		} else if strings.Contains(idLower, "o1") || strings.Contains(idLower, "o3") {
			ctx = 200000
			maxOut = 65536
			thinking = "medium"
			budget = 4096
		} else if strings.Contains(idLower, "reasoner") || strings.Contains(idLower, "r1") || strings.Contains(idLower, "qwen") {
			ctx = 256000
			maxOut = 65536
			thinking = "high"
			budget = 8192
		}

		models = append(models, ModelConfig{
			ID:              id,
			Alias:           id,
			Enabled:         true,
			ContextTokens:   ctx,
			MaxOutputTokens: maxOut,
			ThinkingMode:    thinking,
			ThinkingBudget:  budget,
		})
	}

	json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"models":  models,
	})
}

type TestModelReq struct {
	Provider string `json:"provider"`
	APIKey   string `json:"api_key"`
	BaseURL  string `json:"base_url"`
	Model    string `json:"model"`
}

func handleTestModel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	var req TestModelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "参数非法"})
		return
	}

	if req.APIKey == "" {
		json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "请先输入 API Key"})
		return
	}

	start := time.Now()
	provider := strings.ToLower(req.Provider)
	baseURL := strings.TrimRight(req.BaseURL, "/")

	mockPrompt := dto.GeneralOpenAIRequest{
		Model: req.Model,
		Messages: []dto.Message{
			{Role: "user", Content: "Hi"},
		},
	}

	client := &http.Client{Timeout: 15 * time.Second}

	if provider == "claude" {
		if baseURL == "" {
			baseURL = "https://api.anthropic.com"
		}
		meta := &convmeta.Values{
			OriginModelName:     req.Model,
			UpstreamModelName:   req.Model,
			ChannelMetaAttached: true,
			Options: &convmeta.Options{
				Claude: convmeta.ClaudeOptions{
					DefaultMaxTokens: func(model string) int { return 32 },
				},
			},
		}
		cRes, err := relayconvert.ConvertRequest(context.Background(), meta, types.RelayFormatClaude, &mockPrompt)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "协议转换错误: " + err.Error()})
			return
		}
		payload, _ := json.Marshal(cRes.Value)

		httpReq, _ := http.NewRequest(http.MethodPost, baseURL+"/v1/messages", bytes.NewReader(payload))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("x-api-key", req.APIKey)
		httpReq.Header.Set("anthropic-version", "2023-06-01")

		resp, err := client.Do(httpReq)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "网络超时: " + err.Error()})
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			json.NewEncoder(w).Encode(map[string]any{"success": false, "error": fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body))})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"success":    true,
			"latency_ms": time.Since(start).Milliseconds(),
		})
		return
	}

	if baseURL == "" {
		if provider == "deepseek" {
			baseURL = "https://api.deepseek.com"
		} else {
			baseURL = "https://api.openai.com"
		}
	}
	payload, _ := json.Marshal(mockPrompt)
	httpReq, _ := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(payload))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)

	resp, err := client.Do(httpReq)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "网络超时: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		json.NewEncoder(w).Encode(map[string]any{"success": false, "error": fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body))})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"success":    true,
		"latency_ms": time.Since(start).Milliseconds(),
	})
}

func handleGetConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	globalConfig.mu.RLock()
	defer globalConfig.mu.RUnlock()
	json.NewEncoder(w).Encode(map[string]any{
		"success":  true,
		"channels": globalConfig.Channels,
	})
}

func handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodPost {
		http.Error(w, `{"success":false,"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Channels []ChannelConfig `json:"channels"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "配置 JSON 格式非法: " + err.Error()})
		return
	}

	globalConfig.mu.Lock()
	globalConfig.Channels = req.Channels
	globalConfig.mu.Unlock()

	if err := saveConfig(); err != nil {
		json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "保存本地配置失败: " + err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]any{"success": true, "message": "配置已成功保存并即时生效！"})
}

type ConvertReq struct {
	SourceFormat string          `json:"source_format"`
	TargetFormat string          `json:"target_format"`
	OriginModel  string          `json:"origin_model"`
	TargetModel  string          `json:"target_model"`
	Payload      json.RawMessage `json:"payload"`
}

type ConvertResp struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Quality string `json:"quality,omitempty"`
	Result  any    `json:"result,omitempty"`
	Steps   any    `json:"steps,omitempty"`
}

func handleConvert(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(ConvertResp{Success: false, Error: "Method not allowed"})
		return
	}

	var req ConvertReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(ConvertResp{Success: false, Error: "请求体 JSON 格式非法: " + err.Error()})
		return
	}

	parsed, err := parseRequestByFormat(req.SourceFormat, req.Payload)
	if err != nil {
		json.NewEncoder(w).Encode(ConvertResp{Success: false, Error: err.Error()})
		return
	}

	originModel := req.OriginModel
	if originModel == "" {
		originModel = "gpt-4o"
	}
	targetModel := req.TargetModel
	if targetModel == "" {
		targetModel = "claude-3-5-sonnet"
	}

	meta := &convmeta.Values{
		OriginModelName:     originModel,
		UpstreamModelName:   targetModel,
		ChannelMetaAttached: true,
		Options: &convmeta.Options{
			Claude: convmeta.ClaudeOptions{
				DefaultMaxTokens: func(model string) int { return 4096 },
			},
		},
	}

	srcFmt := mapTargetFormat(req.SourceFormat)
	targetFmt := mapTargetFormat(req.TargetFormat)

	// 同协议直接透传，无需冗余转换
	if srcFmt == targetFmt {
		json.NewEncoder(w).Encode(ConvertResp{
			Success: true,
			Quality: "passthrough",
			Result:  parsed,
			Steps: []map[string]string{{
				"Converter": "direct_passthrough (同协议原生直通透传)",
				"From":      req.SourceFormat,
				"To":        req.TargetFormat,
			}},
		})
		return
	}

	res, err := relayconvert.ConvertRequest(context.Background(), meta, targetFmt, parsed)
	if err != nil {
		json.NewEncoder(w).Encode(ConvertResp{Success: false, Error: "格式转换失败: " + err.Error()})
		return
	}

	json.NewEncoder(w).Encode(ConvertResp{
		Success: true,
		Quality: string(res.Quality),
		Result:  res.Value,
		Steps:   res.Steps,
	})
}

func parseRequestByFormat(format string, raw []byte) (any, error) {
	var rawMap map[string]any
	_ = json.Unmarshal(raw, &rawMap)

	// 智能结构特征检测：当用户输入的 JSON 与所选标签格式不同时自动适配
	if _, hasMessages := rawMap["messages"]; hasMessages {
		if _, hasContents := rawMap["contents"]; !hasContents {
			if _, hasSystem := rawMap["system"]; hasSystem && strings.ToLower(format) == "claude" {
				var req dto.ClaudeRequest
				if err := json.Unmarshal(raw, &req); err == nil && len(req.Messages) > 0 {
					return &req, nil
				}
			}
			var req dto.GeneralOpenAIRequest
			if err := json.Unmarshal(raw, &req); err == nil && len(req.Messages) > 0 {
				return &req, nil
			}
		}
	}
	if _, hasInput := rawMap["input"]; hasInput {
		var req dto.OpenAIResponsesRequest
		if err := json.Unmarshal(raw, &req); err == nil {
			return &req, nil
		}
	}
	if _, hasContents := rawMap["contents"]; hasContents {
		var req dto.GeminiChatRequest
		if err := json.Unmarshal(raw, &req); err == nil {
			return &req, nil
		}
	}

	switch strings.ToLower(format) {
	case "openai", "openai_chat":
		var req dto.GeneralOpenAIRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("解析 OpenAI 请求失败: %w", err)
		}
		return &req, nil
	case "claude":
		var req dto.ClaudeRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("解析 Claude 请求失败: %w", err)
		}
		return &req, nil
	case "gemini":
		var req dto.GeminiChatRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("解析 Gemini 请求失败: %w", err)
		}
		return &req, nil
	case "openai_responses":
		var req dto.OpenAIResponsesRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("解析 Responses 请求失败: %w", err)
		}
		return &req, nil
	default:
		var req dto.GeneralOpenAIRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("默认解析 OpenAI 请求失败: %w", err)
		}
		return &req, nil
	}
}

func mapTargetFormat(f string) types.RelayFormat {
	switch strings.ToLower(f) {
	case "claude":
		return types.RelayFormatClaude
	case "gemini":
		return types.RelayFormatGemini
	case "openai_responses":
		return types.RelayFormatOpenAIResponses
	default:
		return types.RelayFormatOpenAI
	}
}

func handleV1Models(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	globalConfig.mu.RLock()
	defer globalConfig.mu.RUnlock()

	var models []map[string]any
	seen := make(map[string]bool)

	for _, ch := range globalConfig.Channels {
		for _, m := range ch.Models {
			if m.Enabled {
				idToExpose := m.Alias
				if idToExpose == "" {
					idToExpose = m.ID
				}
				if !seen[idToExpose] {
					seen[idToExpose] = true
					models = append(models, map[string]any{
						"id":              idToExpose,
						"object":          "model",
						"owned_by":        ch.Provider,
						"context_window":  m.ContextTokens,
						"max_output":      m.MaxOutputTokens,
						"thinking_mode":   m.ThinkingMode,
						"thinking_budget": m.ThinkingBudget,
					})
				}
			}
		}
	}

	if len(models) == 0 {
		models = []map[string]any{
			{"id": "gpt-4o", "object": "model", "owned_by": "openai"},
			{"id": "claude-3-7-sonnet", "object": "model", "owned_by": "claude"},
			{"id": "deepseek-chat", "object": "model", "owned_by": "deepseek"},
		}
	}

	json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   models,
	})
}

func handleV1ChatCompletions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":{"message":"Method not allowed"}}`, http.StatusMethodNotAllowed)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":{"message":"Failed to read request"}}`, http.StatusBadRequest)
		return
	}

	var openAIReq dto.GeneralOpenAIRequest
	if err := json.Unmarshal(bodyBytes, &openAIReq); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"Invalid JSON: %s"}}`, err.Error()), http.StatusBadRequest)
		return
	}

	ch, mCfg := findChannelAndModel(openAIReq.Model)
	if ch == nil || ch.APIKey == "" {
		replyText := fmt.Sprintf("【LLMDock 协议转换网关已成功连通】\n接收到请求，模型: `%s`，消息数: %d 条。\n\n提示：当前未在 Web UI 中配置该模型的 API Key。请打开 http://127.0.0.1:3000 进入【渠道与模型配置】填入对应 API Key 并启用模型，即可实时发起真实对话。", openAIReq.Model, len(openAIReq.Messages))
		resp := dto.OpenAITextResponse{
			Id:      fmt.Sprintf("chatcmpl-llmdock-%d", time.Now().Unix()),
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   openAIReq.Model,
			Choices: []dto.OpenAITextResponseChoice{
				{
					Index: 0,
					Message: dto.Message{
						Role:    "assistant",
						Content: replyText,
					},
					FinishReason: "stop",
				},
			},
			Usage: dto.Usage{
				PromptTokens:     10,
				CompletionTokens: 30,
				TotalTokens:      40,
			},
		}
		json.NewEncoder(w).Encode(resp)
		return
	}

	// 映射到上游实际模型 ID 与思考参数
	upstreamModelID := openAIReq.Model
	maxOutput := 65536
	thinkingEffort := ""
	thinkingBudget := 0

	if mCfg != nil {
		if mCfg.ID != "" {
			upstreamModelID = mCfg.ID
		}
		if mCfg.MaxOutputTokens > 0 {
			maxOutput = mCfg.MaxOutputTokens
		}
		thinkingBudget = mCfg.ThinkingBudget
		switch mCfg.ThinkingMode {
		case "off", "none", "":
			thinkingEffort = ""
		case "minimal":
			thinkingEffort = "low"
			if thinkingBudget <= 0 {
				thinkingBudget = 2048
			}
		case "low":
			thinkingEffort = "low"
			if thinkingBudget <= 0 {
				thinkingBudget = 4096
			}
		case "medium":
			thinkingEffort = "medium"
			if thinkingBudget <= 0 {
				thinkingBudget = 8192
			}
		case "high":
			thinkingEffort = "high"
			if thinkingBudget <= 0 {
				thinkingBudget = 16384
			}
		case "max", "ultra":
			thinkingEffort = "high"
			if thinkingBudget <= 0 {
				thinkingBudget = 64000
			}
		case "auto":
			thinkingEffort = "medium"
		default: // "custom", "budget"
			thinkingEffort = "medium"
		}
	}

	if thinkingEffort != "" {
		openAIReq.ReasoningEffort = thinkingEffort
		if thinkingBudget > 0 {
			rawReasoning, _ := json.Marshal(map[string]any{"max_tokens": thinkingBudget})
			openAIReq.Reasoning = rawReasoning
		}
	}

	if strings.ToLower(ch.Provider) == "claude" {
		meta := &convmeta.Values{
			OriginModelName:     openAIReq.Model,
			UpstreamModelName:   upstreamModelID,
			ChannelMetaAttached: true,
			ReasoningEffort:      thinkingEffort,
			Options: &convmeta.Options{
				Claude: convmeta.ClaudeOptions{
					ThinkingAdapterEnabled: true,
					DefaultMaxTokens: func(model string) int { return maxOutput },
				},
			},
		}
		claudeRes, err := relayconvert.ConvertRequest(context.Background(), meta, types.RelayFormatClaude, &openAIReq)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":{"message":"Convert to Claude failed: %s"}}`, err.Error()), http.StatusInternalServerError)
			return
		}

		claudePayload, _ := json.Marshal(claudeRes.Value)
		baseURL := ch.BaseURL
		if baseURL == "" {
			baseURL = "https://api.anthropic.com"
		}
		targetEndpoint := strings.TrimSuffix(baseURL, "/") + "/v1/messages"

		httpReq, _ := http.NewRequest(http.MethodPost, targetEndpoint, bytes.NewReader(claudePayload))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("x-api-key", ch.APIKey)
		httpReq.Header.Set("anthropic-version", "2023-06-01")

		client := &http.Client{Timeout: 120 * time.Second}
		httpResp, err := client.Do(httpReq)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":{"message":"Upstream request failed: %s"}}`, err.Error()), http.StatusBadGateway)
			return
		}
		defer httpResp.Body.Close()

		respBytes, _ := io.ReadAll(httpResp.Body)
		if httpResp.StatusCode != http.StatusOK {
			w.WriteHeader(httpResp.StatusCode)
			w.Write(respBytes)
			return
		}

		var claudeResp dto.ClaudeResponse
		if err := json.Unmarshal(respBytes, &claudeResp); err != nil {
			w.WriteHeader(httpResp.StatusCode)
			w.Write(respBytes)
			return
		}

		convertedResp, err := relayconvert.ConvertResponse(context.Background(), meta, types.RelayFormatOpenAI, &claudeResp)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":{"message":"Convert response failed: %s"}}`, err.Error()), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(convertedResp.Value)
		return
	}

	// 默认 OpenAI / DeepSeek / 通用
	baseURL := ch.BaseURL
	if baseURL == "" {
		if strings.ToLower(ch.Provider) == "deepseek" {
			baseURL = "https://api.deepseek.com"
		} else {
			baseURL = "https://api.openai.com"
		}
	}
	targetEndpoint := strings.TrimSuffix(baseURL, "/") + "/v1/chat/completions"
	openAIReq.Model = upstreamModelID
	payload, _ := json.Marshal(openAIReq)

	httpReq, _ := http.NewRequest(http.MethodPost, targetEndpoint, bytes.NewReader(payload))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+ch.APIKey)

	client := &http.Client{Timeout: 120 * time.Second}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"Upstream request failed: %s"}}`, err.Error()), http.StatusBadGateway)
		return
	}
	defer httpResp.Body.Close()

	w.WriteHeader(httpResp.StatusCode)
	io.Copy(w, httpResp.Body)
}

func main() {
	loadConfig()

	mux := http.NewServeMux()

	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			handleGetConfig(w, r)
		} else {
			handleSaveConfig(w, r)
		}
	})
	mux.HandleFunc("/api/fetch_models", handleFetchModels)
	mux.HandleFunc("/api/test_model", handleTestModel)
	mux.HandleFunc("/api/convert", handleConvert)

	mux.HandleFunc("/v1/models", handleV1Models)
	mux.HandleFunc("/v1/chat/completions", handleV1ChatCompletions)

	mux.HandleFunc("/app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Write([]byte(jsApp))
	})

	tmpl := template.Must(template.New("index").Parse(htmlIndex))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		tmpl.Execute(w, nil)
	})

	addr := "127.0.0.1:3000"
	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	fmt.Printf("\n======================================================\n")
	fmt.Printf("  ⚓ LLMDock 智能大模型网关与拓展坞已就绪！\n")
	fmt.Printf("  • 可视化配置后台: http://%s\n", addr)
	fmt.Printf("  • IDE 接入 Base URL: http://%s/v1\n", addr)
	fmt.Printf("  • 零外部依赖: 纯 Go 原生内核，内存占用 ~10MB\n")
	fmt.Printf("======================================================\n\n")

	log.Fatal(server.ListenAndServe())
}
