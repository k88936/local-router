package main

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
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
		log.Printf("ERROR: Failed to encode models response: %v", err)
	}
}

func (s *Server) ForwardRequest(w http.ResponseWriter, r *http.Request) {
	log.Printf("Received %s request to %s from %s", r.Method, r.URL.Path, r.RemoteAddr)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("ERROR: Failed to read request body: %v", err)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var requestBody map[string]interface{}
	if err := json.Unmarshal(body, &requestBody); err != nil {
		log.Printf("ERROR: Failed to parse request body: %v", err)
		http.Error(w, "Failed to parse request body", http.StatusBadRequest)
		return
	}

	modelName, ok := requestBody["model"].(string)
	if !ok {
		log.Printf("ERROR: Model not specified in request")
		http.Error(w, "Model not specified", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	provider := s.FindProvider(modelName)
	s.mu.RUnlock()

	if provider == nil {
		log.Printf("ERROR: Provider not found for model: %s", modelName)
		http.Error(w, "Provider not found for model: "+modelName, http.StatusBadRequest)
		return
	}

	clientRequestedStream, _ := requestBody["stream"].(bool)

	actualModelName := s.GetActualModelName(modelName)
	requestBody["model"] = actualModelName
	requestBody["stream"] = true

	newBody, err := json.Marshal(requestBody)
	if err != nil {
		log.Printf("ERROR: Failed to marshal request body: %v", err)
		http.Error(w, "Failed to marshal request body", http.StatusInternalServerError)
		return
	}

	targetURL, err := url.Parse(provider.URL)
	if err != nil {
		log.Printf("ERROR: Invalid provider URL %s: %v", provider.URL, err)
		http.Error(w, "Invalid provider URL", http.StatusInternalServerError)
		return
	}

	targetURL.Path += "/chat/completions"
	targetURL.RawQuery = r.URL.RawQuery

	req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL.String(), bytes.NewBuffer(newBody))
	if err != nil {
		log.Printf("ERROR: Failed to create request: %v", err)
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
		log.Printf("ERROR: Failed to forward request to provider: %v", err)
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
		log.Printf("ERROR: Failed to reload config: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error":   "Failed to reload config",
			"details": err.Error(),
		})
		return
	}

	if err := newConfig.Validate(); err != nil {
		log.Printf("ERROR: Config validation failed during reload: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error":   "Config validation failed",
			"details": err.Error(),
		})
		return
	}

	s.config = newConfig
	log.Printf("Successfully reloaded configuration for %d providers", len(s.config.Providers))

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
		log.Printf("ERROR: Failed to write OpenAPI spec: %v", err)
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
				log.Printf("WARNING: Failed to parse chunk: %v", err)
				continue
			}

			chunkCount++
			if firstResponse == nil {
				firstResponse = chunk
			}

			if choices, ok := chunk["choices"].([]interface{}); ok && len(choices) > 0 {
				if choice, ok := choices[0].(map[string]interface{}); ok {
					if delta, ok := choice["delta"].(map[string]interface{}); ok {
						if content, ok := delta["content"].(string); ok {
							fullContent.WriteString(content)
						}

						if isClientStreaming {
							reconstructedChunk := make(map[string]interface{})
							for k, v := range chunk {
								reconstructedChunk[k] = v
							}

							if id, ok := chunk["id"].(string); ok {
								reconstructedChunk["id"] = id
							} else if id, ok := chunk["trace_id"].(string); ok {
								reconstructedChunk["id"] = id
							}

							reconstructedChunk["object"] = "chat.completion.chunk"
							reconstructedChunk["model"] = modelName

							if toolCalls, ok := delta["tool_calls"].([]interface{}); ok && len(toolCalls) > 0 {
								if call, ok := toolCalls[0].(map[string]interface{}); ok {
									call["id"] = reconstructedChunk["id"]
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
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("ERROR: Scanner error during stream processing: %v", err)
	}

	if !isClientStreaming && firstResponse != nil {
		if choices, ok := firstResponse["choices"].([]interface{}); ok && len(choices) > 0 {
			if choice, ok := choices[0].(map[string]interface{}); ok {
				if _, ok := choice["delta"]; ok {
					delete(choice, "delta")
					choice["message"] = map[string]interface{}{
						"role":    "assistant",
						"content": fullContent.String(),
					}
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(firstResponse); err != nil {
			log.Printf("ERROR: Failed to encode complete response: %v", err)
		} else {
			log.Printf("Successfully sent non-streaming response with %d chunks processed", chunkCount)
		}
	} else if isClientStreaming {
		log.Printf("Successfully sent streaming response with %d chunks processed", chunkCount)
	}
}
