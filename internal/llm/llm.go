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
	"os"
	"strings"
	"time"
)

const (
	// EnvKey is this tool's own key: the value itself, so a provider that is not Ollama does not have
	// to borrow another product's variable name. EnvKeyVar names a variable to read instead, for a
	// machine that already exports OPENAI_API_KEY and would rather point at it than copy it.
	EnvKey    = "EVALCTL_DESCRIBE_KEY"
	EnvKeyVar = "EVALCTL_DESCRIBE_KEY_ENV"
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
// say which file answered without ever saying what it answered with. KeyName is the variable that
// answered, so an error can name it instead of guessing which provider's name was meant.
type Config struct {
	BaseURL string
	Model   string
	APIKey  string
	KeyName string
	KeyFrom string
}

// MissingKeyError is the key's absence, said in one line and with the place to fix it. The detail
// (which variables were looked for, which files were read) is on the type so a shell can print it
// under the line.
type MissingKeyError struct {
	BaseURL string
	Names   []string
	Files   []string
}

func (e *MissingKeyError) Error() string {
	where := "~/.config/evalctl/env"
	if len(e.Files) > 0 {
		where = e.Files[len(e.Files)-1]
	}
	names := e.Names
	if len(names) == 0 {
		names = []string{EnvKey}
	}
	return fmt.Sprintf("no API key for %s — set %s, name the variable that holds one with %s=<NAME>, "+
		"or put one of %s in %s", e.BaseURL, EnvKey, EnvKeyVar, strings.Join(names, " / "), where)
}

// KeyCandidates is the variables a key is looked for, in order: the tool's own value, the variable
// named by EnvKeyVar, the provider's conventional name, and then the names this machine is most
// likely to already carry. A non-Ollama provider is a first-class case, not a borrowed variable name.
func KeyCandidates(baseURL string) []string {
	if name := strings.TrimSpace(os.Getenv(EnvKeyVar)); name != "" {
		return []string{name}
	}
	var out []string
	add := func(name string) {
		for _, have := range out {
			if have == name {
				return
			}
		}
		out = append(out, name)
	}
	add(EnvKey)
	switch {
	case strings.Contains(baseURL, "openai.com"):
		add("OPENAI_API_KEY")
	case strings.Contains(baseURL, "anthropic"):
		add("ANTHROPIC_API_KEY")
	case strings.Contains(baseURL, "openrouter"):
		add("OPENROUTER_API_KEY")
	case strings.Contains(baseURL, "ollama"):
		add("OLLAMA_API_KEY")
	}
	add("OLLAMA_API_KEY")
	add("OPENAI_API_KEY")
	return out
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
	names := KeyCandidates(c.BaseURL)
	for _, name := range names {
		key, from := envValue(name)
		if key == "" {
			continue
		}
		c.APIKey, c.KeyName, c.KeyFrom = key, name, from
		return c, nil
	}
	return Config{}, &MissingKeyError{BaseURL: c.BaseURL, Names: names, Files: searchedEnvFiles()}
}

// keyName is the variable that answered, or the tool's own name when a Config was built by hand
// (a test, or a caller that set the key itself).
func (c Config) keyName() string {
	if c.KeyName != "" {
		return c.KeyName
	}
	return EnvKey
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
		return "", &MissingKeyError{BaseURL: c.BaseURL, Names: KeyCandidates(c.BaseURL), Files: searchedEnvFiles()}
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
		return fmt.Errorf("%s refused the key (HTTP %d) — %s from %s may be wrong or expired", c.BaseURL, code, c.keyName(), c.KeyFrom)
	case http.StatusNotFound:
		return fmt.Errorf("%s has no such model or endpoint (HTTP 404) — check %s=%s and %s", c.BaseURL, EnvModel, c.Model, EnvBaseURL)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%s is rate-limiting this key (HTTP 429) — try again in a minute", c.BaseURL)
	}
	return fmt.Errorf("%s answered HTTP %d: %s", c.BaseURL, code, msg)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
