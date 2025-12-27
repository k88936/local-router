package main

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

//go:embed openapi.json
var openAPISpec []byte

func (s *Server) ModelsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	s.mu.RLock()
	defer s.mu.RUnlock()

	var models []Model
	for _, provider := range s.config.Providers {
		for _, model := range provider.Models {
			models = append(models, Model{
				ID:     "[" + provider.Name + "]" + model,
				Object: "model",
			})
		}
	}

	response := ModelsResponse{
		Object: "list",
		Data:   models,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		GetLogger().Error("Failed to encode models response: %v", err)
	}
}

func (s *Server) ForwardRequest(w http.ResponseWriter, r *http.Request) {
	GetLogger().Info("Received %s request to %s from %s", r.Method, r.URL.Path, r.RemoteAddr)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		GetLogger().Error("Failed to read request body: %v", err)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var requestBodyMap map[string]interface{}
	if err := json.Unmarshal(body, &requestBodyMap); err != nil {
		GetLogger().Error("Failed to parse request body: %v", err)
		http.Error(w, "Failed to parse request body", http.StatusBadRequest)
		return
	}

	var request ChatCompletionRequest
	if err := request.FromMap(requestBodyMap); err != nil {
		GetLogger().Error("Failed to convert request body: %v", err)
		http.Error(w, "Failed to convert request body", http.StatusBadRequest)
		return
	}

	modelName := request.Model
	if modelName == "" {
		GetLogger().Error("Model not specified in request")
		http.Error(w, "Model not specified", http.StatusBadRequest)
		return
	}

	provider := s.FindProvider(modelName)

	if provider == nil {
		GetLogger().Error("Provider not found for model: %s", modelName)
		http.Error(w, "Provider not found for model: "+modelName, http.StatusBadRequest)
		return
	}

	clientRequestedStream := request.Stream

	actualModelName := s.GetActualModelName(modelName)

	// Update the request for forwarding
	forwardRequest := request.ToMap()
	forwardRequest["model"] = actualModelName
	forwardRequest["stream"] = true

	// Log the last user message from the conversation history
	for i := len(request.Messages) - 1; i >= 0; i-- {
		if request.Messages[i].Role == "user" {
			GetLogger().Info("Last user message: %s", request.Messages[i].Content)
			break
		}
	}

	newBody, err := json.Marshal(forwardRequest)
	if err != nil {
		GetLogger().Error("Failed to marshal request body: %v", err)
		http.Error(w, "Failed to marshal request body", http.StatusInternalServerError)
		return
	}

	targetURL, err := url.Parse(provider.URL)
	if err != nil {
		GetLogger().Error("Invalid provider URL %s: %v", provider.URL, err)
		http.Error(w, "Invalid provider URL", http.StatusInternalServerError)
		return
	}

	targetURL.Path += "/chat/completions"
	targetURL.RawQuery = r.URL.RawQuery

	req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL.String(), bytes.NewBuffer(newBody))
	if err != nil {
		GetLogger().Error("Failed to create request: %v", err)
		http.Error(w, "Failed to create request", http.StatusInternalServerError)
		return
	}

	for name, headers := range r.Header {
		for _, h := range headers {
			req.Header.Add(name, h)
		}
	}
	req.Header.Set("Authorization", "Bearer "+provider.Secret)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		GetLogger().Error("Failed to forward request to provider: %v", err)
		http.Error(w, "Failed to forward request", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	for name, headers := range resp.Header {
		for _, h := range headers {
			w.Header().Add(name, h)
		}
	}

	s.HandleStreamResponse(w, resp.Body, clientRequestedStream, resp.StatusCode, modelName)
}

func (s *Server) ConfigReloadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	s.mu.Lock()
	defer s.mu.Unlock()

	newConfig, err := loadConfig(s.configPath)
	if err != nil {
		GetLogger().Error("Failed to reload config: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error":   "Failed to reload config",
			"details": err.Error(),
		})
		return
	}

	if err := newConfig.Validate(); err != nil {
		GetLogger().Error("Config validation failed during reload: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error":   "Config validation failed",
			"details": err.Error(),
		})
		return
	}

	s.config = newConfig
	GetLogger().Info("Successfully reloaded configuration for %d providers", len(s.config.Providers))

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":   "Configuration reloaded successfully",
		"providers": len(s.config.Providers),
	})
}

func (s *Server) OpenAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	if _, err := w.Write(openAPISpec); err != nil {
		GetLogger().Error("Failed to write OpenAPI spec: %v", err)
		http.Error(w, "Failed to write OpenAPI spec", http.StatusInternalServerError)
	}
}

func (s *Server) HandleStreamResponse(w http.ResponseWriter, body io.ReadCloser, isClientStreaming bool, statusCode int, modelName string) {
	scanner := bufio.NewScanner(body)
	var fullContent strings.Builder
	var firstResponse map[string]interface{}
	chunkCount := 0
	flusher, _ := w.(http.Flusher)

	if isClientStreaming {
		w.WriteHeader(statusCode)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
	}

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			dataStr := strings.TrimPrefix(line, "data:")
			if dataStr == "[DONE]" {
				if isClientStreaming {
					fmt.Fprint(w, "data: [DONE]\n\n")
					flusher.Flush()
				}
				break
			}

			var chunk map[string]interface{}
			if err := json.Unmarshal([]byte(dataStr), &chunk); err != nil {
				GetLogger().Warn("Failed to parse chunk: %v", err)
				continue
			}

			chunkCount++
			if firstResponse == nil {
				firstResponse = chunk
			}

			var responseChunk ChatCompletionResponse
			if err := responseChunk.FromMap(chunk); err != nil {
				GetLogger().Warn("Failed to parse chunk: %v", err)
				continue
			}

			if len(responseChunk.Choices) > 0 && responseChunk.Choices[0].Delta != nil {
				delta := responseChunk.Choices[0].Delta
				if delta.Content != "" {
					fullContent.WriteString(delta.Content)
				}

				if isClientStreaming {
					reconstructedChunk := chunk

					// Set ID
					if responseChunk.ID != "" {
						reconstructedChunk["id"] = responseChunk.ID
					} else if traceID, ok := chunk["trace_id"].(string); ok {
						reconstructedChunk["id"] = traceID
					}

					reconstructedChunk["object"] = "chat.completion.chunk"
					reconstructedChunk["model"] = modelName

					// Handle tool calls if needed
					if len(delta.ToolCalls) > 0 {
						if choices, ok := reconstructedChunk["choices"].([]interface{}); ok && len(choices) > 0 {
							if choice, ok := choices[0].(map[string]interface{}); ok {
								if deltaMap, ok := choice["delta"].(map[string]interface{}); ok {
									if toolCalls, ok := deltaMap["tool_calls"].([]interface{}); ok && len(toolCalls) > 0 {
										if call, ok := toolCalls[0].(map[string]interface{}); ok {
											call["id"] = reconstructedChunk["id"]
										}
									}
								}
							}
						}
					}

					if reconstructedData, err := json.Marshal(reconstructedChunk); err == nil {
						fmt.Fprintf(w, "data: %s\n\n", string(reconstructedData))
						flusher.Flush()
					}
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		GetLogger().Error("Scanner error during stream processing: %v", err)
	}

	if firstResponse != nil {
		var finalResponse ChatCompletionResponse
		if err := finalResponse.FromMap(firstResponse); err != nil {
			GetLogger().Warn("Failed to parse final response: %v", err)
		} else if len(finalResponse.Choices) > 0 && finalResponse.Choices[0].Delta != nil {
			// Convert delta to message for non-streaming response
			finalResponse.Choices[0].Delta = nil
			finalResponse.Choices[0].Message = &ChatMessage{
				Role:    "assistant",
				Content: fullContent.String(),
			}
		}

		if isClientStreaming {
			GetLogger().Info("Assistant response: %s", fullContent.String())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		responseData := finalResponse.ToMap()
		responseData["choices"] = firstResponse["choices"] // Keep original choices structure
		if err := json.NewEncoder(w).Encode(responseData); err != nil {
			GetLogger().Error("Failed to encode complete response: %v", err)
		} else {
			GetLogger().Info("Successfully sent non-streaming response with %d chunks processed", chunkCount)
		}
	}
}
