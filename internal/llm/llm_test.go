package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The key policy is worth more tests than the call: a key in the tree is a key on GitHub, and the
// only thing standing between the two is this file.

func TestParseEnvFileReadsWhatPeopleWrite(t *testing.T) {
	text := strings.Join([]string{
		"# the description model's key",
		"",
		`OLLAMA_API_KEY="sk-abc 123"`,
		"export EVALCTL_DESCRIBE_MODEL='gpt-oss:120b'",
		"PLAIN=value",
		"TRAILING=value # this is a comment",
		"FRAGMENT=https://x.test/p#anchor",
		"EMPTY=",
		"NOT A LINE",
	}, "\r\n")
	got := parseEnvFile(text)
	want := map[string]string{
		"OLLAMA_API_KEY":         "sk-abc 123",
		"EVALCTL_DESCRIBE_MODEL": "gpt-oss:120b",
		"PLAIN":                  "value",
		"TRAILING":               "value",
		"FRAGMENT":               "https://x.test/p#anchor",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := got["EMPTY"]; ok {
		t.Error("an empty value should count as unset")
	}
}

// The environment wins: a stale .env must not beat the key somebody just exported.
func TestTheEnvironmentBeatsTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("OLLAMA_API_KEY=from-the-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvFileVar, path)
	t.Setenv(EnvKey, "from-the-environment")
	if v, from := envValue(EnvKey); v != "from-the-environment" || from != "the environment" {
		t.Fatalf("got %q from %q", v, from)
	}
}

func TestTheFileAnswersWhenTheEnvironmentIsSilent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "evalctl.env")
	if err := os.WriteFile(path, []byte(EnvKey+"=from-the-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvFileVar, path)
	t.Setenv(EnvKey, "")
	v, from := envValue(EnvKey)
	if v != "from-the-file" {
		t.Fatalf("value = %q", v)
	}
	if !strings.HasSuffix(from, "evalctl.env") {
		t.Fatalf("source = %q, want the file", from)
	}
}

func TestFromEnvSaysWhereTheKeyCameFrom(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("OLLAMA_API_KEY=k\nEVALCTL_DESCRIBE_MODEL=test-model\nEVALCTL_DESCRIBE_BASE_URL=http://127.0.0.1:11434/v1/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvFileVar, path)
	t.Setenv(EnvKey, "")
	t.Setenv(EnvModel, "")
	t.Setenv(EnvBaseURL, "")
	c, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if c.Model != "test-model" {
		t.Errorf("model = %q", c.Model)
	}
	// The trailing slash is not part of the path: the request is base + "/chat/completions".
	if c.BaseURL != "http://127.0.0.1:11434/v1" || c.ChatURL() != "http://127.0.0.1:11434/v1/chat/completions" {
		t.Errorf("base = %q, url = %q", c.BaseURL, c.ChatURL())
	}
	if c.KeyFrom == "" {
		t.Error("the source of the key is what the screen says; it must not be empty")
	}
}

func TestMissingKeyNamesTheVariableAndTheFix(t *testing.T) {
	t.Setenv(EnvFileVar, filepath.Join(t.TempDir(), "absent"))
	t.Setenv(EnvKey, "")
	_, err := FromEnv()
	if err == nil {
		t.Fatal("a missing key must not resolve")
	}
	if !strings.Contains(err.Error(), EnvKey) || !strings.Contains(err.Error(), "evalctl") {
		t.Errorf("the error has to name the variable and the file: %v", err)
	}
	if strings.Contains(err.Error(), "\n") {
		t.Errorf("the status row is one line, so the error is: %v", err)
	}
}

func TestCompletePostsTheChatShape(t *testing.T) {
	var got chatRequest
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"  the new description  "}}]}`))
	}))
	defer srv.Close()

	c := Config{BaseURL: srv.URL, Model: "m", APIKey: "k", KeyFrom: "test"}
	text, err := c.Complete(context.Background(), "system line", "user prompt", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if text != "the new description" {
		t.Errorf("text = %q, want it trimmed", text)
	}
	if auth != "Bearer k" {
		t.Errorf("auth = %q", auth)
	}
	if got.Model != "m" || len(got.Messages) != 2 || got.Messages[0].Content != "system line" {
		t.Errorf("request = %+v", got)
	}
	if got.Stream {
		t.Error("streaming is not this call's shape")
	}
}

func TestCompleteExplainsARefusedKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	c := Config{BaseURL: srv.URL, Model: "m", APIKey: "k", KeyFrom: "~/.config/evalctl/env"}
	_, err := c.Complete(context.Background(), "s", "u", time.Second)
	if err == nil {
		t.Fatal("a 401 is not an answer")
	}
	for _, want := range []string{"refused the key", EnvKey, "~/.config/evalctl/env"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "Bearer") {
		t.Error("the key must never reach an error message")
	}
}

func TestCompleteReportsAnEmptyAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"","reasoning":"thinking…"},"finish_reason":"length"}]}`))
	}))
	defer srv.Close()

	c := Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}
	if _, err := c.Complete(context.Background(), "s", "u", time.Second); err == nil {
		t.Fatal("an empty answer is an error, not a proposal")
	}
}

// TestAProviderThatIsNotOllamaIsNotAskedForOllamasVariable: the describe call is one HTTP request,
// so its key is configuration. An OpenAI user points at their own key instead of renaming it.
func TestTheDescribeKeyIsNotAnOllamaVariable(t *testing.T) {
	t.Setenv(EnvFileVar, filepath.Join(t.TempDir(), "nothing-here"))
	t.Setenv(EnvKey, "")
	t.Setenv(EnvBaseURL, "https://api.openai.com/v1")
	t.Setenv("OPENAI_API_KEY", "sk-from-openai")
	c, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if c.APIKey != "sk-from-openai" || c.KeyName != "OPENAI_API_KEY" {
		t.Fatalf("key = %q from %q, want OPENAI_API_KEY", c.APIKey, c.KeyName)
	}

	// A variable the user already exports is named, not copied.
	t.Setenv(EnvKeyVar, "MY_PROVIDER_KEY")
	t.Setenv("MY_PROVIDER_KEY", "from-my-variable")
	c, err = FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if c.APIKey != "from-my-variable" || c.KeyName != "MY_PROVIDER_KEY" {
		t.Fatalf("key = %q from %q, want MY_PROVIDER_KEY", c.APIKey, c.KeyName)
	}

	// With no candidate at all the error names what it looked for, so the fix is on the page.
	t.Setenv(EnvKeyVar, "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OLLAMA_API_KEY", "")
	_, err = FromEnv()
	if err == nil || !strings.Contains(err.Error(), "EVALCTL_DESCRIBE_KEY") ||
		!strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatalf("a missing key must name the variables it looked for, got %v", err)
	}
}
