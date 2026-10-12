package transform

import (
	"encoding/json"
	"testing"

	"github.com/dvcrn/antigravity-oauth-proxy/internal/openai"
)

func TestParallelToolContinuationSignatures(t *testing.T) {
	for _, signatures := range [][2]string{{"", ""}, {"genuine-signature", ""}, {"", "signed-sibling"}, {"genuine-signature", "signed-sibling"}} {
		req := openai.ChatCompletionRequest{Model: "gemini-3.1-pro-high", Messages: []openai.Message{{Role: "assistant", ToolCalls: []openai.OpenAIToolCall{
			{ID: "call_a", Function: openai.OpenAIFunctionCall{Name: "read", Arguments: "{}"}},
			{ID: "call_b", Function: openai.OpenAIFunctionCall{Name: "read", Arguments: "{}"}},
		}}}}
		want := "skip_thought_signature_validator"
		for i, signature := range signatures {
			if signature != "" {
				req.Messages[0].ToolCalls[i].ID += "|" + signature
			}
		}
		if signatures[0] != "" {
			want = signatures[0]
		}
		got, err := ToGeminiRequest(&req, "fixture")
		if err != nil {
			t.Fatal(err)
		}
		parts := got.Request.Contents[0].Parts
		if parts[0].ThoughtSignature != want || parts[1].ThoughtSignature != signatures[1] {
			t.Fatalf("parallel signatures = %q, %q", parts[0].ThoughtSignature, parts[1].ThoughtSignature)
		}
	}
}

func TestToolContinuationSignatures(t *testing.T) {
	for _, tc := range []struct{ name, model, id, extra, want string }{
		{"foreign history", "gemini-3.1-pro-high", "call_fixture", "", "skip_thought_signature_validator"},
		{"legacy signature", "gemini-3.1-pro-high", "call_fixture|genuine-signature", "", "genuine-signature"},
		{"structured signature", "gemini-3.1-pro-high", "call_fixture", `,"extra_content":{"google":{"thought_signature":"structured-signature"}}`, "structured-signature"},
		{"structured signature takes precedence", "gemini-3.1-pro-high", "call_fixture|legacy-signature", `,"extra_content":{"google":{"thought_signature":"structured-signature"}}`, "structured-signature"},
		{"empty metadata retains legacy", "gemini-3.1-pro-high", "call_fixture|legacy-signature", `,"extra_content":{"google":{"thought_signature":""}}`, "legacy-signature"},
		{"other model", "claude-sonnet", "call_fixture", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var req openai.ChatCompletionRequest
			raw := `{"messages":[{"role":"user","content":"Read fixture"},{"role":"assistant","tool_calls":[{"id":"` + tc.id + `","type":"function","function":{"name":"read","arguments":"{}"}` + tc.extra + `}]},{"role":"tool","tool_call_id":"` + tc.id + `","content":"OK"}]}`
			if err := json.Unmarshal([]byte(raw), &req); err != nil {
				t.Fatal(err)
			}
			req.Model = tc.model
			got, err := ToGeminiRequest(&req, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			part := got.Request.Contents[1].Parts[0]
			if part.ThoughtSignature != tc.want {
				t.Fatalf("signature = %q, want %q", part.ThoughtSignature, tc.want)
			}
		})
	}
}
