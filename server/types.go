package main

import (
	"sync"
	"time"
)

type Provider struct {
	Name   string   `yaml:"name"`
	URL    string   `yaml:"url"`
	Secret string   `yaml:"secret"`
	Models []string `yaml:"models"`
}

type Config struct {
	Port      int        `yaml:"port"`
	LogLevel  string     `yaml:"logLevel"`
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

type ChatCompletionRequest struct {
	Model    string                 `json:"model"`
	Messages []ChatMessage          `json:"messages"`
	Stream   bool                   `json:"stream"`
	Extra    map[string]interface{} `json:"-"`
}

func (r *ChatCompletionRequest) ToMap() map[string]interface{} {
	result := make(map[string]interface{})
	result["model"] = r.Model
	result["messages"] = r.Messages
	result["stream"] = r.Stream

	for k, v := range r.Extra {
		result[k] = v
	}
	return result
}

func (r *ChatCompletionRequest) FromMap(data map[string]interface{}) error {
	// Handle required fields
	if model, ok := data["model"].(string); ok {
		r.Model = model
	}

	if stream, ok := data["stream"].(bool); ok {
		r.Stream = stream
	}

	// Handle messages
	if messages, ok := data["messages"].([]interface{}); ok {
		for _, msg := range messages {
			if msgMap, ok := msg.(map[string]interface{}); ok {
				message := ChatMessage{}
				if role, ok := msgMap["role"].(string); ok {
					message.Role = role
				}
				if content, ok := msgMap["content"].(string); ok {
					message.Content = content
				}
				r.Messages = append(r.Messages, message)
			}
		}
	}

	// Store extra fields
	r.Extra = make(map[string]interface{})
	for k, v := range data {
		if k != "model" && k != "messages" && k != "stream" {
			r.Extra[k] = v
		}
	}

	return nil
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatCompletionChoice struct {
	Index        int                    `json:"index"`
	Message      *ChatMessage           `json:"message,omitempty"`
	Delta        *ChatMessageDelta      `json:"delta,omitempty"`
	FinishReason string                 `json:"finish_reason,omitempty"`
	Extra        map[string]interface{} `json:"-"`
}

type ChatMessageDelta struct {
	Role      string     `json:"role,omitempty"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function map[string]interface{} `json:"function"`
}

type ChatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   map[string]interface{} `json:"usage,omitempty"`
	Extra   map[string]interface{} `json:"-"`
}

func (r *ChatCompletionResponse) FromMap(data map[string]interface{}) error {
	if id, ok := data["id"].(string); ok {
		r.ID = id
	} else if traceID, ok := data["trace_id"].(string); ok {
		r.ID = traceID
	} else {
		panic("chunk missing field: id")
	}

	if object, ok := data["object"].(string); ok {
		r.Object = object
	} else {
		r.Object = "chat.completion.chunk"
	}

	if created, ok := data["created"].(float64); ok {
		r.Created = int64(created)
	} else {
		r.Created = time.Now().Unix()
	}

	if model, ok := data["model"].(string); ok {
		r.Model = model
	}

	// Handle choices
	if choices, ok := data["choices"].([]interface{}); ok {
		for _, choice := range choices {
			if choiceMap, ok := choice.(map[string]interface{}); ok {
				choiceStruct := ChatCompletionChoice{}
				if index, ok := choiceMap["index"].(float64); ok {
					choiceStruct.Index = int(index)
				}
				if finishReason, ok := choiceMap["finish_reason"].(string); ok {
					choiceStruct.FinishReason = finishReason
				}

				// Handle delta
				if delta, ok := choiceMap["delta"].(map[string]interface{}); ok {
					deltaStruct := ChatMessageDelta{}
					if role, ok := delta["role"].(string); ok {
						deltaStruct.Role = role
					}
					if content, ok := delta["content"].(string); ok {
						deltaStruct.Content = content
					}
					choiceStruct.Delta = &deltaStruct
				}

				r.Choices = append(r.Choices, choiceStruct)
			}
		}
	}

	// Store extra fields
	r.Extra = make(map[string]interface{})
	for k, v := range data {
		if k != "id" && k != "object" && k != "created" && k != "model" && k != "choices" && k != "usage" {
			r.Extra[k] = v
		}
	}

	return nil
}

func (r *ChatCompletionResponse) ToMap() map[string]interface{} {
	result := make(map[string]interface{})
	result["id"] = r.ID
	result["object"] = r.Object
	result["created"] = r.Created
	result["model"] = r.Model

	// Convert choices
	choices := make([]interface{}, len(r.Choices))
	for i, choice := range r.Choices {
		choiceMap := make(map[string]interface{})
		choiceMap["index"] = choice.Index
		choiceMap["finish_reason"] = choice.FinishReason

		if choice.Message != nil {
			choiceMap["message"] = map[string]interface{}{
				"role":    choice.Message.Role,
				"content": choice.Message.Content,
			}
		}

		if choice.Delta != nil {
			deltaMap := make(map[string]interface{})
			deltaMap["role"] = choice.Delta.Role
			deltaMap["content"] = choice.Delta.Content
			if len(choice.Delta.ToolCalls) > 0 {
				deltaMap["tool_calls"] = choice.Delta.ToolCalls
			}
			choiceMap["delta"] = deltaMap
		}

		choices[i] = choiceMap
	}
	result["choices"] = choices

	// Add usage if present
	if r.Usage != nil {
		result["usage"] = r.Usage
	}

	// Add extra fields
	for k, v := range r.Extra {
		result[k] = v
	}

	return result
}

type Server struct {
	config     *Config
	configPath string
	mu         sync.RWMutex
}

func NewServer(config *Config, configPath string) *Server {
	return &Server{
		config:     config,
		configPath: configPath,
	}
}

func (s *Server) FindProvider(modelName string) *Provider {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for i, provider := range s.config.Providers {
		if len(modelName) > len(provider.Name)+2 &&
			modelName[0] == '[' &&
			modelName[len(provider.Name)+1] == ']' &&
			modelName[1:len(provider.Name)+1] == provider.Name {
			return &s.config.Providers[i]
		}
	}
	return nil
}

func (s *Server) GetActualModelName(modelName string) string {
	if provider := s.FindProvider(modelName); provider != nil {
		return modelName[len(provider.Name)+2:]
	}
	return modelName
}

// Helper functions for safe type conversion
func getString(m map[string]interface{}, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

func getBool(m map[string]interface{}, key string) bool {
	if val, ok := m[key].(bool); ok {
		return val
	}
	return false
}

func getFloat64(m map[string]interface{}, key string) float64 {
	if val, ok := m[key].(float64); ok {
		return val
	}
	return 0
}

func getMap(m map[string]interface{}, key string) map[string]interface{} {
	if val, ok := m[key].(map[string]interface{}); ok {
		return val
	}
	return nil
}

func getSlice(m map[string]interface{}, key string) []interface{} {
	if val, ok := m[key].([]interface{}); ok {
		return val
	}
	return nil
}
