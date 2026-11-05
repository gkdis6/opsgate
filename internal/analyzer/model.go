package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type function struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type toolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function function `json:"function"`
}
type message struct {
	Role    string     `json:"role"`
	Content string     `json:"content"`
	Calls   []toolCall `json:"tool_calls,omitempty"`
	CallID  string     `json:"tool_call_id,omitempty"`
}
type definition struct {
	Type     string `json:"type"`
	Function struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Parameters  any    `json:"parameters"`
	} `json:"function"`
}
type modelReply struct {
	Message message
	Tokens  int
}
type modelClient interface {
	Complete(context.Context, []message, []definition, int) (modelReply, error)
}
type chatModel struct {
	client              *http.Client
	endpoint, key, name string
	maxBytes            int
}

func (m chatModel) Complete(ctx context.Context, messages []message, tools []definition, maxTokens int) (modelReply, error) {
	request := struct {
		Model     string       `json:"model"`
		Messages  []message    `json:"messages"`
		Tools     []definition `json:"tools,omitempty"`
		MaxTokens int          `json:"max_completion_tokens"`
		Parallel  bool         `json:"parallel_tool_calls"`
		Store     bool         `json:"store"`
	}{m.name, messages, tools, maxTokens, false, false}
	body, err := json.Marshal(request)
	if err != nil || len(body) > m.maxBytes {
		return modelReply{}, fmt.Errorf("model request exceeds context byte limit")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint, bytes.NewReader(body))
	if err != nil {
		return modelReply{}, fmt.Errorf("invalid model endpoint")
	}
	req.Header.Set("Authorization", "Bearer "+m.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return modelReply{}, fmt.Errorf("model request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return modelReply{}, fmt.Errorf("model returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil || len(raw) > 1<<20 {
		return modelReply{}, fmt.Errorf("invalid model response size")
	}
	var result struct {
		Choices []struct {
			Message message `json:"message"`
			Finish  string  `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			Total int `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &result) != nil || len(result.Choices) != 1 || result.Usage.Total < 1 {
		return modelReply{}, fmt.Errorf("invalid model response or missing token usage")
	}
	choice := result.Choices[0]
	if choice.Finish != "stop" && choice.Finish != "tool_calls" {
		return modelReply{}, fmt.Errorf("model did not complete its response")
	}
	return modelReply{choice.Message, result.Usage.Total}, nil
}

func newModel(c Config) (modelClient, error) {
	if c.Model.Backend == "codex_cli" {
		return newCodex(c)
	}
	if c.Model.Backend == "claude_cli" {
		return newClaude(c)
	}
	apiKey, err := credential(c.Model.APIKeyFile)
	if err != nil {
		return nil, fmt.Errorf("model.api_key_file: %w", err)
	}
	return chatModel{client: httpClient(c.Timeout), endpoint: c.Model.Endpoint, key: apiKey, name: c.Model.Name, maxBytes: c.MaxContextBytes}, nil
}
