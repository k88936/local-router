package main

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

type ChatCompletionRequest struct {
	Model    string                 `json:"model"`
	Messages []ChatMessage          `json:"messages"`
	Stream   bool                   `json:"stream"`
	Extra    map[string]interface{} `json:"-"`
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

type Server struct {
	config *Config
}

func NewServer(config *Config) *Server {
	return &Server{
		config: config,
	}
}

func (s *Server) FindProvider(modelName string) *Provider {
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
