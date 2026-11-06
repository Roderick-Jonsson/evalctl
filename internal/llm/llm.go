// Package llm is the one place evalctl talks to a model provider itself.
//
// The rest of the tool never does: a trigger run, a judge and a reviser are all `hermes` one-shots in
// their own sandbox profile, and that stays the case because those calls have to read skills and
// evals with the harness's own containment rules. This call is different — it rewrites a single
// paragraph from a handful of text it is handed — so it is one HTTP request with a key, no agent
// loop, no tools, nothing to read off disk it was not given.
//
// The provider is whatever answers the OpenAI-compatible chat completions shape, which is what Ollama
// Cloud serves at https://ollama.com/v1. Base URL, model and key are all configuration, so pointing
// this at a local Ollama, an OpenAI key or a proxy is an environment variable and not a code change.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// EnvKey is the standard variable Ollama Cloud reads, and the one the harness already uses.
	EnvKey = "OLLAMA_API_KEY"
	// EnvBaseURL and EnvModel are this tool's own knobs, so a different model or a local server does
	// not mean editing a file.
	EnvBaseURL = "EVALCTL_DESCRIBE_BASE_URL"
	EnvModel   = "EVALCTL_DESCRIBE_MODEL"

	DefaultBaseURL = "https://ollama.com/v1"
	// DefaultModel is the model a proposal is written with, and it is not the harness's own.
	//
	// This call is prose, not reasoning: it rewrites one paragraph from a handful of quoted lines. On
	// the same evidence (dummy@#6, two too-broad failures, a 99-character wording) the harness's fast
	// model returned 147 to 321 characters, adding a positive condition the failures never asked for
	// and lifting query words on both runs of the last sweep, which the checks then had to repair.
	// gpt-oss:120b returned 91 to 108 characters, clean after at most one repair, on all three runs.
	// The reasoning models on the same account need the 4000-token ceiling to finish at all — at 2400
	// they spend it thinking and hand back empty content. With the ceiling, kimi-k3.1-pro answered 3
	// runs out of 3 (90 to 140 characters, one call each, clean) and is the best alternative on offer;
	// minimax-m3 and deepseek-v4-pro answered once each (104, 123). glm-5.3 answered empty in two of
	// its three runs, which is why the empty answer is an error with a sentence in it rather than a
	// silent no-op: whoever points this at a reasoning model has to be able to tell what happened.
	//
	// It is a knob, not a policy: EVALCTL_DESCRIBE_MODEL or --model changes it per lane or per call.
	DefaultModel = "gpt-oss:120b"
)

// Config is one provider, resolved. KeyFrom is a path or "the environment": it exists so a screen can
// say which file answered without ever saying what it answered with.
type Config struct {
	BaseURL string
	Model   string
	APIKey  string
	KeyFrom string
}

// MissingKeyError is the key's absence, said in one line and with the place to fix it. The detail
// (which files were read) is on the type so a shell can print it under the line.
type MissingKeyError struct {
	Files []string
}

func (e *MissingKeyError) Error() string {
	add := "~/.config/evalctl/env"
	if len(e.Files) > 0 {
		add = e.Files[len(e.Files)-1]
	}
	return fmt.Sprintf("%s is not set — add `%s=…` to %s, or export it", EnvKey, EnvKey, add)
}

// FromEnv resolves the provider: the key from the environment or an env file, the base URL and the
// model from the environment with Ollama Cloud's own defaults.
func FromEnv() (Config, error) {
	c := Config{BaseURL: DefaultBaseURL, Model: DefaultModel}
	if v, _ := envValue(EnvBaseURL); v != "" {
		c.BaseURL = v
	}
	if v, _ := envValue(EnvModel); v != "" {
		c.Model = v
	}
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	key, from := envValue(EnvKey)
	if key == "" {
		return Config{}, &MissingKeyError{Files: searchedEnvFiles()}
	}
	c.APIKey, c.KeyFrom = key, from
	return c, nil
}

// ChatURL is where one completion is posted.
func (c Config) ChatURL() string { return c.BaseURL + "/chat/completions" }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Stream      bool          `json:"stream"`
	Temperature float64       `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content   string `json:"content"`
			Reasoning string `json:"reasoning"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// Complete is one call: a system line, a user prompt, and the model's text back.
//
// The request is deliberately plain — no tools, no streaming, low temperature, a token ceiling — so
// the answer is a paragraph and the cost is a paragraph. A model that spends the ceiling on reasoning
// and hands back nothing is an error rather than an empty proposal: the caller has to be able to say
// why it has nothing.
func (c Config) Complete(ctx context.Context, system, user string, budget time.Duration) (string, error) {
	if c.APIKey == "" {
		return "", &MissingKeyError{Files: searchedEnvFiles()}
	}
	if budget <= 0 {
		budget = 3 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	payload, err := json.Marshal(chatRequest{
		Model: c.Model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Stream:      false,
		Temperature: 0.3,
		MaxTokens:   4000,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ChatURL(), bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: budget}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return "", fmt.Errorf("the call was cancelled")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return "", fmt.Errorf("no answer within %s — %s did not finish", budget, c.BaseURL)
		}
		return "", fmt.Errorf("cannot reach %s: %v", c.BaseURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("reading the reply: %v", err)
	}
	if resp.StatusCode/100 != 2 {
		return "", statusError(resp.StatusCode, body, c)
	}
	var out chatResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("%s answered in a shape this tool does not read: %v", c.BaseURL, err)
	}
	if out.Error != nil && strings.TrimSpace(out.Error.Message) != "" {
		return "", fmt.Errorf("%s: %s", c.Model, oneLine(out.Error.Message))
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("%s answered with no choices", c.Model)
	}
	text := strings.TrimSpace(out.Choices[0].Message.Content)
	if text == "" {
		return "", fmt.Errorf("%s answered with no text — it may have spent the token budget on reasoning", c.Model)
	}
	return text, nil
}

// statusError turns a provider's refusal into the next thing to do: a refused key, a model that is
// not on the account, and a rate limit are three different mornings.
func statusError(code int, body []byte, c Config) error {
	msg := oneLine(string(body))
	if len(msg) > 240 {
		msg = msg[:240] + "…"
	}
	var out chatResponse
	if json.Unmarshal(body, &out) == nil && out.Error != nil && strings.TrimSpace(out.Error.Message) != "" {
		msg = oneLine(out.Error.Message)
	}
	switch code {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%s refused the key (HTTP %d) — %s from %s may be wrong or expired", c.BaseURL, code, EnvKey, c.KeyFrom)
	case http.StatusNotFound:
		return fmt.Errorf("%s has no such model or endpoint (HTTP 404) — check %s=%s and %s", c.BaseURL, EnvModel, c.Model, EnvBaseURL)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%s is rate-limiting this key (HTTP 429) — try again in a minute", c.BaseURL)
	}
	return fmt.Errorf("%s answered HTTP %d: %s", c.BaseURL, code, msg)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
