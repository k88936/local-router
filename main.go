package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Provider struct {
	Name   string   `yaml:"name"`
	URL    string   `yaml:"url"`
	Secret string   `yaml:"secret"`
	Models []string `yaml:"models"`
}

type Config struct {
	Port      int        `yaml:"port"`
	Providers []Provider `yaml:"providers"`
}

type Model struct {
	ID     string `json:"id"`
	Object string `json:"object"`
}

type ModelsResponse struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

var config Config

func loadConfig(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return fmt.Errorf("failed to parse config file: %w", err)
	}

	return nil
}

func findProvider(modelName string) *Provider {
	for i, provider := range config.Providers {
		if strings.HasPrefix(modelName, "["+provider.Name+"]") {
			return &config.Providers[i]
		}
	}
	return nil
}

func getActualModelName(modelName string) string {
	if provider := findProvider(modelName); provider != nil {
		return strings.TrimPrefix(modelName, "["+provider.Name+"]")
	}
	return modelName
}

func modelsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var models []Model
	for _, provider := range config.Providers {
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

func forwardRequest(w http.ResponseWriter, r *http.Request) {
	log.Printf("Received %s request to %s from %s", r.Method, r.URL.Path, r.RemoteAddr)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("ERROR: Failed to read request body: %v", err)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	r.Body.Close()

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

	provider := findProvider(modelName)
	if provider == nil {
		log.Printf("ERROR: Provider not found for model: %s", modelName)
		http.Error(w, "Provider not found for model: "+modelName, http.StatusBadRequest)
		return
	}

	clientRequestedStream, _ := requestBody["stream"].(bool)

	actualModelName := getActualModelName(modelName)
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

	req, err := http.NewRequest(r.Method, targetURL.String(), bytes.NewBuffer(newBody))
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

	handleStreamResponse(w, resp.Body, clientRequestedStream, resp.StatusCode, modelName)
}

func handleStreamResponse(w http.ResponseWriter, body io.ReadCloser, isClientStreaming bool, statusCode int, modalName string) {
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
			log.Printf(dataStr)
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

							if id, ok := chunk["trace_id"].(string); ok {
								reconstructedChunk["id"] = id
							}
							//
							reconstructedChunk["object"] = "chat.completion.chunk"

							reconstructedChunk["modal"] = modalName

							// ensure id in tool_calls
							if toolCalls, ok := delta["tool_calls"].([]interface{}); ok {
								if call, ok := toolCalls[0].(map[string]interface{}); ok {
									call["id"] = reconstructedChunk["id"]
								}
							}

							if reconstructedData, err := json.Marshal(reconstructedChunk); err == nil {
								fmt.Fprintf(w, "data: %s\n\n", string(reconstructedData))
								log.Printf(string(reconstructedData))
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

func loggingMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("ENDPOINT CALLED: %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		next.ServeHTTP(w, r)
	}
}

func logAllRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("REQUEST ATTEMPT: %s %s from %s - User-Agent: %s", r.Method, r.URL.Path, r.RemoteAddr, r.UserAgent())
		next.ServeHTTP(w, r)
	})
}

func main() {
	configPaths := []string{
		os.Getenv("HOME") + "/.local-router.yaml",
		os.Getenv("USERPROFILE") + "/.local-router.yaml",
		"./.local-router.yaml",
	}

	var configErr error
	for _, path := range configPaths {
		if _, err := os.Stat(path); err == nil {
			configErr = loadConfig(path)
			if configErr == nil {
				break
			}
		}
	}

	if configErr != nil {
		panic("Failed to load .local-router.yaml from HOME, USERPROFILE, or current directory")
	}
	log.Printf("Loaded configuration for %d providers", len(config.Providers))
	for i, provider := range config.Providers {
		log.Printf("Provider %d: %s with %d models", i+1, provider.Name, len(provider.Models))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", loggingMiddleware(modelsHandler))
	mux.HandleFunc("/v1/chat/completions", loggingMiddleware(forwardRequest))

	addr := fmt.Sprintf(":%d", config.Port)
	log.Printf("Starting server on port %d", config.Port)
	log.Printf("Server ready to accept connections")
	log.Fatal(http.ListenAndServe(addr, logAllRequests(mux)))
}
