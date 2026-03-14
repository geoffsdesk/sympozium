package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// maxToolIterations is the maximum number of tool-call round-trips before
// the agent stops and returns whatever text it has.
const maxToolIterations = 25

type agentResult struct {
	Status   string `json:"status"`
	Response string `json:"response,omitempty"`
	Error    string `json:"error,omitempty"`
	Metrics  struct {
		DurationMs   int64 `json:"durationMs"`
		InputTokens  int   `json:"inputTokens"`
		OutputTokens int   `json:"outputTokens"`
		ToolCalls    int   `json:"toolCalls"`
	} `json:"metrics"`
}

type streamChunk struct {
	Type    string `json:"type"`
	Content string `json:"content"`
	Index   int    `json:"index"`
}

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.Println("agent-runner starting")

	task := getEnv("TASK", "")
	if task == "" {
		if b, err := os.ReadFile("/ipc/input/task.json"); err == nil {
			var input struct {
				Task string `json:"task"`
			}
			if json.Unmarshal(b, &input) == nil && input.Task != "" {
				task = input.Task
			}
		}
	}
	if task == "" {
		fatal("TASK env var is empty and no /ipc/input/task.json found")
	}

	systemPrompt := getEnv("SYSTEM_PROMPT", "You are a helpful AI assistant.")
	provider := strings.ToLower(getEnv("MODEL_PROVIDER", "vertexai"))
	modelName := getEnv("MODEL_NAME", "gemini-2.0-flash")
	baseURL := strings.TrimRight(getEnv("MODEL_BASE_URL", ""), "/")
	memoryEnabled := getEnv("MEMORY_ENABLED", "") == "true"
	toolsEnabled := getEnv("TOOLS_ENABLED", "") == "true"

	// Load skill files and build enhanced system prompt.
	skills := loadSkills(defaultSkillsDir)
	systemPrompt = buildSystemPrompt(systemPrompt, skills, toolsEnabled)

	// If this run was triggered from a channel, inject context so the
	// agent knows how to reply through the originating channel.
	sourceChannel := getEnv("SOURCE_CHANNEL", "")
	sourceChatID := getEnv("SOURCE_CHAT_ID", "")
	if sourceChannel != "" {
		channelCtx := fmt.Sprintf(
			"\n\n## Channel Context\n\n"+
				"This task was received through the **%s** channel (chat ID: %s). "+
				"You can reply through this channel using the `send_channel_message` tool "+
				"with channel=%q and chatId=%q. Use it to deliver results, ask follow-up "+
				"questions, or send notifications to the user.",
			sourceChannel, sourceChatID, sourceChannel, sourceChatID,
		)
		systemPrompt += channelCtx
		log.Printf("channel context injected: channel=%s chatId=%s", sourceChannel, sourceChatID)
	}

	// Resolve tool definitions.
	var tools []ToolDef
	if toolsEnabled {
		tools = defaultTools()
		log.Printf("tools enabled: %d tool(s) registered", len(tools))
	}

	// Read existing memory if available.
	var memoryContent string
	if memoryEnabled {
		if b, err := os.ReadFile("/memory/MEMORY.md"); err == nil {
			memoryContent = strings.TrimSpace(string(b))
			log.Printf("loaded memory (%d bytes)", len(memoryContent))
		}
	}

	// Prepend memory context to the task if present.
	if memoryContent != "" && memoryContent != "# Agent Memory\n\nNo memories recorded yet." {
		task = fmt.Sprintf("## Your Memory\nThe following is your persistent memory from prior interactions:\n\n%s\n\n## Current Task\n%s", memoryContent, task)
	}

	// If memory is enabled, add memory instructions to system prompt.
	if memoryEnabled {
		memoryInstruction := "\n\nYou have persistent memory. After completing your task, " +
			"output a memory update block wrapped in markers like this:\n" +
			"__SYMPOZIUM_MEMORY__\n<your updated MEMORY.md content>\n__SYMPOZIUM_MEMORY_END__\n" +
			"Include key facts, preferences, and context from this and past interactions. " +
			"Keep it concise (under 256KB). Use markdown format."
		systemPrompt += memoryInstruction
	}

	apiKey := firstNonEmpty(
		os.Getenv("API_KEY"),
		os.Getenv("GOOGLE_API_KEY"),
		os.Getenv("VERTEX_AI_API_KEY"),
	)

	log.Printf("provider=%s model=%s baseURL=%s tools=%v task=%q",
		provider, modelName, baseURL, toolsEnabled, truncate(task, 80))

	_ = os.MkdirAll("/ipc/output", 0o755)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	obs := initObservability(ctx)
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := obs.shutdown(shutdownCtx); err != nil {
			log.Printf("failed to shutdown OTel providers: %v", err)
		}
	}()

	ctx, runSpan := obs.startRunSpan(ctx,
		attribute.String("instance", getEnv("INSTANCE_NAME", "")),
		attribute.String("tenant.namespace", getEnv("AGENT_NAMESPACE", "")),
		attribute.String("model", modelName),
		attribute.String("task.summary", truncate(task, 200)),
	)
	writeTraceContextMetadata(ctx)
	logWithTrace(ctx, "info", "agent run started", map[string]any{
		"instance":  getEnv("INSTANCE_NAME", ""),
		"namespace": getEnv("AGENT_NAMESPACE", ""),
		"provider":  provider,
		"model":     modelName,
	})

	start := time.Now()

	var (
		responseText string
		inputTokens  int
		outputTokens int
		toolCalls    int
		err          error
	)

	switch provider {
	case "vertexai", "gemini":
		responseText, inputTokens, outputTokens, toolCalls, err = callGemini(ctx, apiKey, baseURL, modelName, systemPrompt, task, tools)
	default:
		responseText, inputTokens, outputTokens, toolCalls, err = callGemini(ctx, apiKey, baseURL, modelName, systemPrompt, task, tools)
	}

	elapsed := time.Since(start)

	var res agentResult
	res.Metrics.DurationMs = elapsed.Milliseconds()
	res.Metrics.ToolCalls = toolCalls

	debugMode := getEnv("DEBUG", "") == "true"

	if err != nil {
		log.Printf("LLM call failed: %v", err)
		res.Status = "error"
		res.Error = err.Error()
		markSpanError(runSpan, err)
		runSpan.SetStatus(codes.Error, err.Error())
	} else {
		log.Printf("LLM call succeeded (tokens: in=%d out=%d, tool_calls=%d)", inputTokens, outputTokens, toolCalls)
		res.Status = "success"
		res.Response = responseText
		res.Metrics.InputTokens = inputTokens
		res.Metrics.OutputTokens = outputTokens
		runSpan.SetAttributes(
			attribute.Int("gen_ai.usage.input_tokens", inputTokens),
			attribute.Int("gen_ai.usage.output_tokens", outputTokens),
			attribute.Int("gen_ai.tool.call.count", toolCalls),
		)
		runSpan.SetStatus(codes.Ok, "")
	}

	// Extract and emit memory update before stripping markers from the response.
	if memoryEnabled && res.Response != "" {
		if memUpdate := extractMemoryUpdate(res.Response); memUpdate != "" {
			fmt.Fprintf(os.Stdout, "\n__SYMPOZIUM_MEMORY__%s__SYMPOZIUM_MEMORY_END__\n", memUpdate)
			log.Printf("emitted memory update (%d bytes)", len(memUpdate))
		}
	}

	// Strip memory markers from the response so they don't appear in the
	// TUI feed or channel messages. Keep them only if DEBUG is enabled.
	if !debugMode && res.Response != "" {
		res.Response = stripMemoryMarkers(res.Response)
	}

	if res.Response != "" {
		writeJSON("/ipc/output/stream-0.json", streamChunk{
			Type:    "text",
			Content: res.Response,
			Index:   0,
		})
	}

	writeJSON("/ipc/output/result.json", res)

	// Signal sidecars (tool-executor, etc.) to exit by writing a done sentinel.
	_ = os.WriteFile("/ipc/done", []byte("done"), 0o644)

	// Print a structured marker to stdout so the controller can extract
	// the result from pod logs even after the IPC volume is gone.
	if markerBytes, err := json.Marshal(res); err == nil {
		fmt.Fprintf(os.Stdout, "\n__SYMPOZIUM_RESULT__%s__SYMPOZIUM_END__\n", string(markerBytes))
	}

	if res.Status == "error" {
		obs.recordRunMetrics(ctx, "error", getEnv("INSTANCE_NAME", ""), modelName, getEnv("AGENT_NAMESPACE", ""), elapsed.Milliseconds(), inputTokens, outputTokens)
		logWithTrace(ctx, "error", "agent run failed", map[string]any{"error": res.Error})
		runSpan.End()
		log.Printf("agent-runner finished with error: %s", res.Error)
		os.Exit(1)
	}
	obs.recordRunMetrics(ctx, "success", getEnv("INSTANCE_NAME", ""), modelName, getEnv("AGENT_NAMESPACE", ""), elapsed.Milliseconds(), inputTokens, outputTokens)
	logWithTrace(ctx, "info", "agent run succeeded", map[string]any{
		"duration_ms":   elapsed.Milliseconds(),
		"input_tokens":  inputTokens,
		"output_tokens": outputTokens,
		"tool_calls":    toolCalls,
	})
	runSpan.End()
	log.Println("agent-runner finished successfully")
}

// callGemini uses the Vertex AI Gemini REST API with optional tool calling.
func callGemini(ctx context.Context, apiKey, baseURL, model, systemPrompt, task string, tools []ToolDef) (string, int, int, int, error) {
	// Determine endpoint
	endpoint := baseURL
	if endpoint == "" {
		projectID := getEnv("GCP_PROJECT_ID", "")
		location := getEnv("GCP_LOCATION", "us-central1")
		if projectID != "" {
			endpoint = fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/%s", location, projectID, location, model)
		} else {
			// Fallback to Gemini API (generativelanguage.googleapis.com)
			endpoint = fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s", model)
		}
	}

	client := &http.Client{Timeout: 5 * time.Minute}

	// Build Gemini tool declarations
	var geminiTools []map[string]any
	if len(tools) > 0 {
		var funcDecls []map[string]any
		for _, t := range tools {
			funcDecls = append(funcDecls, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Parameters,
			})
		}
		geminiTools = []map[string]any{
			{"functionDeclarations": funcDecls},
		}
	}

	// Build initial contents
	contents := []map[string]any{
		{
			"role":  "user",
			"parts": []map[string]any{{"text": task}},
		},
	}

	totalInputTokens := 0
	totalOutputTokens := 0
	totalToolCalls := 0

	for i := 0; i < maxToolIterations; i++ {
		// Build request body
		reqBody := map[string]any{
			"contents": contents,
			"systemInstruction": map[string]any{
				"parts": []map[string]any{{"text": systemPrompt}},
			},
			"generationConfig": map[string]any{
				"maxOutputTokens": 8192,
			},
		}
		if len(geminiTools) > 0 {
			reqBody["tools"] = geminiTools
		}

		bodyBytes, err := json.Marshal(reqBody)
		if err != nil {
			return "", totalInputTokens, totalOutputTokens, totalToolCalls,
				fmt.Errorf("marshalling request: %w", err)
		}

		// Build URL
		requestURL := endpoint + ":generateContent"
		if apiKey != "" {
			if strings.Contains(requestURL, "?") {
				requestURL += "&key=" + apiKey
			} else {
				requestURL += "?key=" + apiKey
			}
		}

		chatCtx, chatSpan := obs.startChatSpan(ctx,
			attribute.String("gen_ai.system", "vertexai"),
			attribute.String("gen_ai.request.model", model),
		)

		req, err := http.NewRequestWithContext(chatCtx, http.MethodPost, requestURL, bytes.NewReader(bodyBytes))
		if err != nil {
			markSpanError(chatSpan, err)
			chatSpan.End()
			return "", totalInputTokens, totalOutputTokens, totalToolCalls,
				fmt.Errorf("creating request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		// Use access token for Vertex AI (service account)
		accessToken := getEnv("GOOGLE_ACCESS_TOKEN", "")
		if accessToken != "" {
			req.Header.Set("Authorization", "Bearer "+accessToken)
		}

		resp, err := client.Do(req)
		if err != nil {
			markSpanError(chatSpan, err)
			chatSpan.End()
			return "", totalInputTokens, totalOutputTokens, totalToolCalls,
				fmt.Errorf("Vertex AI API error: %w", err)
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			markSpanError(chatSpan, err)
			chatSpan.End()
			return "", totalInputTokens, totalOutputTokens, totalToolCalls,
				fmt.Errorf("reading response: %w", err)
		}

		if resp.StatusCode != 200 {
			markSpanError(chatSpan, fmt.Errorf("HTTP %d", resp.StatusCode))
			chatSpan.End()
			return "", totalInputTokens, totalOutputTokens, totalToolCalls,
				fmt.Errorf("Vertex AI API error (HTTP %d): %s", resp.StatusCode, truncate(string(respBody), 500))
		}

		// Parse response
		var geminiResp struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text         string          `json:"text,omitempty"`
						FunctionCall *struct {
							Name string          `json:"name"`
							Args json.RawMessage `json:"args"`
						} `json:"functionCall,omitempty"`
					} `json:"parts"`
					Role string `json:"role"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			UsageMetadata struct {
				PromptTokenCount     int `json:"promptTokenCount"`
				CandidatesTokenCount int `json:"candidatesTokenCount"`
			} `json:"usageMetadata"`
		}

		if err := json.Unmarshal(respBody, &geminiResp); err != nil {
			markSpanError(chatSpan, err)
			chatSpan.End()
			return "", totalInputTokens, totalOutputTokens, totalToolCalls,
				fmt.Errorf("parsing Vertex AI response: %w", err)
		}

		totalInputTokens += geminiResp.UsageMetadata.PromptTokenCount
		totalOutputTokens += geminiResp.UsageMetadata.CandidatesTokenCount
		chatSpan.SetAttributes(
			attribute.Int("gen_ai.usage.input_tokens", geminiResp.UsageMetadata.PromptTokenCount),
			attribute.Int("gen_ai.usage.output_tokens", geminiResp.UsageMetadata.CandidatesTokenCount),
		)

		if len(geminiResp.Candidates) == 0 {
			markSpanError(chatSpan, fmt.Errorf("no candidates in response"))
			chatSpan.End()
			return "", totalInputTokens, totalOutputTokens, totalToolCalls,
				fmt.Errorf("no candidates in Vertex AI response")
		}

		candidate := geminiResp.Candidates[0]
		chatSpan.SetAttributes(attribute.String("gen_ai.response.finish_reasons", candidate.FinishReason))
		chatSpan.SetStatus(codes.Ok, "")
		chatSpan.End()

		// Separate text and function calls
		var textContent strings.Builder
		var functionCalls []struct {
			Name string
			Args json.RawMessage
		}

		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				textContent.WriteString(part.Text)
			}
			if part.FunctionCall != nil {
				functionCalls = append(functionCalls, struct {
					Name string
					Args json.RawMessage
				}{Name: part.FunctionCall.Name, Args: part.FunctionCall.Args})
			}
		}

		// If no function calls, return the text
		if len(functionCalls) == 0 {
			return textContent.String(), totalInputTokens, totalOutputTokens, totalToolCalls, nil
		}

		// Add the model's response to contents
		var modelParts []map[string]any
		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				modelParts = append(modelParts, map[string]any{"text": part.Text})
			}
			if part.FunctionCall != nil {
				modelParts = append(modelParts, map[string]any{
					"functionCall": map[string]any{
						"name": part.FunctionCall.Name,
						"args": json.RawMessage(part.FunctionCall.Args),
					},
				})
			}
		}
		contents = append(contents, map[string]any{
			"role":  "model",
			"parts": modelParts,
		})

		// Execute tool calls and build function responses
		var responseParts []map[string]any
		for _, fc := range functionCalls {
			totalToolCalls++
			log.Printf("function_call [%d]: %s", totalToolCalls, fc.Name)

			result := executeToolCallWithTelemetry(ctx, fc.Name, string(fc.Args), fmt.Sprintf("fc-%d", totalToolCalls))

			responseParts = append(responseParts, map[string]any{
				"functionResponse": map[string]any{
					"name": fc.Name,
					"response": map[string]any{
						"content": result,
					},
				},
			})
		}
		contents = append(contents, map[string]any{
			"role":  "user",
			"parts": responseParts,
		})
	}

	return "", totalInputTokens, totalOutputTokens, totalToolCalls,
		fmt.Errorf("exceeded maximum tool-call iterations (%d)", maxToolIterations)
}

func writeJSON(path string, v any) {
	dir := filepath.Dir(path)
	_ = os.MkdirAll(dir, 0o755)
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Printf("WARNING: failed to marshal JSON for %s: %v", path, err)
		return
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		log.Printf("WARNING: failed to write %s: %v", path, err)
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func fatal(msg string) {
	log.Println("FATAL: " + msg)
	_ = os.MkdirAll("/ipc/output", 0o755)
	_ = os.WriteFile("/ipc/done", []byte("done"), 0o644)
	writeJSON("/ipc/output/result.json", agentResult{
		Status: "error",
		Error:  msg,
	})
	os.Exit(1)
}

// extractMemoryUpdate looks for a memory update block in the LLM response.
// The agent is instructed to wrap its memory updates in:
//
//	__SYMPOZIUM_MEMORY__
//	<content>
//	__SYMPOZIUM_MEMORY_END__
func extractMemoryUpdate(response string) string {
	const startMarker = "__SYMPOZIUM_MEMORY__"
	const endMarker = "__SYMPOZIUM_MEMORY_END__"

	startIdx := strings.LastIndex(response, startMarker)
	if startIdx < 0 {
		return ""
	}
	payload := response[startIdx+len(startMarker):]
	endIdx := strings.Index(payload, endMarker)
	if endIdx < 0 {
		return ""
	}
	return strings.TrimSpace(payload[:endIdx])
}

// stripMemoryMarkers removes all __SYMPOZIUM_MEMORY__...END__ blocks from the
// response text so they don't appear in the TUI feed or channel messages.
func stripMemoryMarkers(response string) string {
	const startMarker = "__SYMPOZIUM_MEMORY__"
	const endMarker = "__SYMPOZIUM_MEMORY_END__"

	for {
		startIdx := strings.Index(response, startMarker)
		if startIdx < 0 {
			break
		}
		endIdx := strings.Index(response[startIdx:], endMarker)
		if endIdx < 0 {
			// Unclosed marker — strip from startMarker to end of string.
			response = strings.TrimSpace(response[:startIdx])
			break
		}
		// Remove the entire marker block.
		response = response[:startIdx] + response[startIdx+endIdx+len(endMarker):]
	}
	return strings.TrimSpace(response)
}
