package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dvcrn/antigravity-oauth-proxy/internal/antigravity"
	"github.com/dvcrn/antigravity-oauth-proxy/internal/credentials"
	"github.com/dvcrn/antigravity-oauth-proxy/internal/openai"
)

type fixtureCredentials struct{}

func (fixtureCredentials) GetCredentials() (*credentials.OAuthCredentials, error) {
	return &credentials.OAuthCredentials{AccessToken: "fixture"}, nil
}
func (fixtureCredentials) SaveCredentials(*credentials.OAuthCredentials) error { return nil }
func (fixtureCredentials) RefreshToken() error                                 { return fmt.Errorf("fixture cannot refresh") }
func (fixtureCredentials) Name() string                                        { return "fixture" }

// Run the actual adapter against a local CloudCode fixture, never Google's API.
func TestToolRoundTrip(t *testing.T) {
	signature := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("fixture-signature"), 8))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "fetchAvailableModels") {
			io.WriteString(w, `{"models":{"gemini-3.1-pro-high":{}}}`)
			return
		}
		var req antigravity.GenerateContentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		var calls []antigravity.ContentPart
		for _, content := range req.Request.Contents {
			for _, part := range content.Parts {
				if part.FunctionCall != nil {
					calls = append(calls, part)
				}
			}
		}
		parts := []map[string]interface{}{
			{"functionCall": map[string]interface{}{"name": "fixture", "args": map[string]interface{}{"slot": 1}}, "thoughtSignature": signature},
			{"functionCall": map[string]interface{}{"name": "fixture", "args": map[string]interface{}{"slot": 2}}},
		}
		if len(calls) == 0 && strings.Contains(req.Request.Contents[0].Parts[0].Text, "preceding") {
			delete(parts[0], "thoughtSignature")
			parts = append([]map[string]interface{}{{"text": "thinking", "thought": true, "thoughtSignature": signature}}, parts...)
		}
		if len(calls) > 0 {
			wantSignature := signature
			if strings.HasPrefix(calls[0].FunctionCall.ID, "call_foreign_") {
				wantSignature = "skip_thought_signature_validator"
			}
			if len(calls) != 2 || calls[0].ThoughtSignature != wantSignature || calls[1].ThoughtSignature != "" {
				w.WriteHeader(400)
				io.WriteString(w, `{"error":{"message":"missing or invalid thought_signature; secret-prompt"}}`)
				return
			}
			parts = []map[string]interface{}{{"text": "DIAG_OK"}}
		}
		payload := map[string]interface{}{"response": map[string]interface{}{"candidates": []interface{}{map[string]interface{}{"content": map[string]interface{}{"parts": parts}}}}}
		if strings.Contains(r.URL.Path, "streamGenerateContent") {
			w.Header().Set("Content-Type", "text/event-stream")
			if len(calls) == 0 && len(parts) == 3 {
				thought := map[string]interface{}{"response": map[string]interface{}{"candidates": []interface{}{map[string]interface{}{"content": map[string]interface{}{"parts": parts[:1]}}}}}
				data, _ := json.Marshal(thought)
				fmt.Fprintf(w, "data: %s\n\n", data)
				payload = map[string]interface{}{"response": map[string]interface{}{"candidates": []interface{}{map[string]interface{}{"content": map[string]interface{}{"parts": parts[1:]}}}}}
			}
			data, _ := json.Marshal(payload)
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
		} else {
			json.NewEncoder(w).Encode(payload)
		}
	}))
	defer upstream.Close()
	previous := antigravity.Endpoints
	antigravity.Endpoints = []string{upstream.URL}
	defer func() { antigravity.Endpoints = previous }()
	t.Setenv("ADMIN_API_KEY", "fixture")
	s := NewServer(fixtureCredentials{}, "fixture")
	if addr := os.Getenv("GEMINI_FIXTURE_ADDR"); addr != "" {
		fixture := &http.Server{Addr: addr, Handler: s, ReadHeaderTimeout: 5 * time.Second}
		defer fixture.Close()
		done := make(chan error, 1)
		go func() { done <- fixture.ListenAndServe() }()
		select {
		case err := <-done:
			t.Fatal(err)
		case <-time.After(90 * time.Second):
			return
		}
	}
	for _, scenario := range []struct{ stream, preceding bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
		stream := scenario.stream
		t.Run(fmt.Sprintf("stream=%v/preceding=%v", stream, scenario.preceding), func(t *testing.T) {
			post := func(messages []map[string]interface{}) *httptest.ResponseRecorder {
				body, _ := json.Marshal(map[string]interface{}{"model": "gemini-3.1-pro-high", "messages": messages, "stream": stream})
				r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
				r.Header.Set("Authorization", "Bearer fixture")
				w := httptest.NewRecorder()
				s.ServeHTTP(w, r)
				if w.Code != 200 {
					t.Fatalf("HTTP %d", w.Code)
				}
				return w
			}
			prompt := "Call fixture twice"
			if scenario.preceding {
				prompt += " with preceding thought signature"
			}
			history := []map[string]interface{}{{"role": "user", "content": prompt}}
			generated := post(history)
			var calls []openai.OpenAIToolCall
			if stream {
				for _, line := range strings.Split(generated.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data: ") || strings.Contains(line, "[DONE]") {
						continue
					}
					var chunk openai.OpenAIChunk
					if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err != nil {
						t.Fatal(err)
					}
					calls = append(calls, chunk.Choices[0].Delta.ToolCalls...)
				}
			} else {
				var result struct {
					Choices []struct {
						Message struct {
							ToolCalls []openai.OpenAIToolCall `json:"tool_calls"`
						} `json:"message"`
					} `json:"choices"`
				}
				if err := json.Unmarshal(generated.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				calls = result.Choices[0].Message.ToolCalls
			}
			if len(calls) != 2 || calls[0].Index != 0 || calls[1].Index != 1 || !strings.HasSuffix(calls[0].ID, "|"+signature) || strings.Contains(calls[1].ID, "|") {
				t.Fatal("parallel indices or signature carriage lost")
			}
			history = append(history, map[string]interface{}{"role": "assistant", "content": nil, "tool_calls": calls})
			for _, call := range calls {
				history = append(history, map[string]interface{}{"role": "tool", "tool_call_id": call.ID, "content": "DIAG_OK"})
			}
			if !strings.Contains(post(history).Body.String(), "DIAG_OK") {
				t.Fatal("continuation failed")
			}
		})
	}
}
