// Package engine talks to the user's own assistant endpoint. It only accepts
// envelope JSON back: model prose is passed through verbatim, and parsing is
// the patch package's job. Secrets never enter the request body.
package engine

import (
	"context"
	"fmt"
	"strings"

	"pondera/internal/boot"
	"pondera/internal/config"
	"pondera/internal/netclient"
	"pondera/internal/provider"
)

// Client is one completion call. Tests inject a fake.
type Client interface {
	Complete(ctx context.Context, system, user string) (string, error)
}

// ProviderClient calls an OpenAI- or Anthropic-compatible endpoint configured
// by the user in the assistant onboarding.
type ProviderClient struct {
	Endpoint   string
	Kind       string
	Model      string
	APIKey     string
	HTTPClient interface{} // reserved; providers build their own transport
}

// Complete runs one non-streaming completion and returns the concatenated text.
func (c *ProviderClient) Complete(ctx context.Context, system, user string) (string, error) {
	if strings.TrimSpace(c.Endpoint) == "" || strings.TrimSpace(c.Model) == "" {
		return "", fmt.Errorf("助手端点或模型未配置")
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return "", fmt.Errorf("助手 API key 未设置")
	}
	kind := strings.ToLower(strings.TrimSpace(c.Kind))
	if kind == "" {
		kind = "openai"
	}
	entry := config.ProviderEntry{
		Name:            "assistant",
		Kind:            kind,
		BaseURL:         c.Endpoint,
		Model:           c.Model,
		MaxOutputTokens: 4096,
	}.WithAPIKeyForProbe(c.APIKey)
	impl, err := boot.NewProviderWithProxy(&entry, netclient.ProxySpec{Mode: netclient.ModeAuto})
	if err != nil {
		return "", fmt.Errorf("构建助手模型客户端失败: %w", err)
	}
	stream, err := impl.Stream(ctx, provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: system},
			{Role: provider.RoleUser, Content: user},
		},
		MaxTokens: 2048,
	})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for chunk := range stream {
		if chunk.Err != nil {
			return "", chunk.Err
		}
		if chunk.Type == provider.ChunkText {
			b.WriteString(chunk.Text)
		}
	}
	return b.String(), nil
}

// CompleteFunc adapts a function to Client (used by tests and the future
// headless CLI entry point).
type CompleteFunc func(ctx context.Context, system, user string) (string, error)

func (f CompleteFunc) Complete(ctx context.Context, system, user string) (string, error) {
	return f(ctx, system, user)
}
