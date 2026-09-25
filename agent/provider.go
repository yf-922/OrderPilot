package agent

import (
	"context"
	"os"
	"strings"
)

type Provider interface {
	Complete(context.Context, CompletionRequest) (Completion, error)
	Name() string
	Model() string
}

func NewProviderFromEnv() Provider {
	apiKey := firstNonEmpty(os.Getenv("AI_API_KEY"), os.Getenv("XAI_API_KEY"), os.Getenv("GROK_API_KEY"))
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("AI_PROVIDER")))
	if provider == "" {
		if apiKey == "" {
			provider = "local"
		} else {
			provider = "grok"
		}
	}
	if provider == "local" || apiKey == "" {
		return NewLocalProvider()
	}
	return NewCompatibleProvider(CompatibleConfig{
		Name:    provider,
		APIKey:  apiKey,
		BaseURL: envOr("AI_BASE_URL", "https://api.x.ai/v1"),
		Model:   envOr("AI_MODEL", "grok-4.6"),
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
