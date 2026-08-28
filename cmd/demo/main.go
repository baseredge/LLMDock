package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/baseredge/llmdock/dto"
	"github.com/baseredge/llmdock/relayconvert"
	"github.com/baseredge/llmdock/relayconvert/convmeta"
	"github.com/baseredge/llmdock/types"
)

func prettyPrint(title string, v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Printf("\n=== %s ===\n%s\n", title, string(b))
}

func main() {
	fmt.Println("==================================================")
	fmt.Println("  LLMDock 多协议格式互转演示 (Zero-Server-Dependency)")
	fmt.Println("==================================================")

	ctx := context.Background()
	maxTokens := uint(2048)
	temp := 0.7

	// 1. 原始输入：标准的 OpenAI Chat Completions 请求
	openAIReq := &dto.GeneralOpenAIRequest{
		Model: "gpt-4o",
		Messages: []dto.Message{
			{Role: "system", Content: "你是一个专业的 Go 语言架构专家。"},
			{Role: "user", Content: "请简单介绍一下 Go 语言的 Channel 机制。"},
		},
		MaxTokens:   &maxTokens,
		Temperature: &temp,
	}
	prettyPrint("【1】原始 OpenAI Chat Completions 请求", openAIReq)

	meta := &convmeta.Values{
		OriginModelName:     "gpt-4o",
		UpstreamModelName:   "claude-3-5-sonnet",
		ChannelMetaAttached: true,
	}

	// 2. 转换成 Claude Messages 请求
	claudeResult, err := relayconvert.ConvertRequest(ctx, meta, types.RelayFormatClaude, openAIReq)
	if err != nil {
		fmt.Printf("转换到 Claude 失败: %v\n", err)
	} else {
		prettyPrint(fmt.Sprintf("【2】转换后的 Claude Messages 格式 (Quality: %s)", claudeResult.Quality), claudeResult.Value)
	}

	// 3. 转换成 Google Gemini generateContent 请求
	geminiResult, err := relayconvert.ConvertRequest(ctx, meta, types.RelayFormatGemini, openAIReq)
	if err != nil {
		fmt.Printf("转换到 Gemini 失败: %v\n", err)
	} else {
		prettyPrint(fmt.Sprintf("【3】转换后的 Google Gemini 格式 (Quality: %s)", geminiResult.Quality), geminiResult.Value)
	}

	// 4. 转换成 OpenAI Responses API 格式
	responsesResult, err := relayconvert.ConvertRequest(ctx, meta, types.RelayFormatOpenAIResponses, openAIReq)
	if err != nil {
		fmt.Printf("转换到 OpenAI Responses 失败: %v\n", err)
	} else {
		prettyPrint(fmt.Sprintf("【4】转换后的 OpenAI Responses 格式 (Quality: %s)", responsesResult.Quality), responsesResult.Value)
	}

	// 5. 模拟下游 Claude 返回的 Response -> 转换为标准 OpenAI Chat Response
	claudeText := "Go 语言的 Channel 是一种用于 Goroutine 之间通信和同步的原语，遵循 CSP 并发模型。"
	claudeResp := &dto.ClaudeResponse{
		Id:   "msg_123456",
		Type: "message",
		Role: "assistant",
		Content: []dto.ClaudeMediaMessage{
			{
				Type: "text",
				Text: &claudeText,
			},
		},
		Model:      "claude-3-5-sonnet",
		StopReason: "end_turn",
		Usage: &dto.ClaudeUsage{
			InputTokens:  25,
			OutputTokens: 50,
		},
	}
	prettyPrint("【5】上游 Claude 返回的原生响应", claudeResp)

	convertedResp, err := relayconvert.ConvertResponse(ctx, meta, types.RelayFormatOpenAI, claudeResp)
	if err != nil {
		fmt.Printf("转换 Response 失败: %v\n", err)
	} else {
		prettyPrint("【6】转回标准 OpenAI Chat Completion 响应", convertedResp.Value)
	}
}
