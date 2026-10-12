package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dvcrn/antigravity-oauth-proxy/internal/antigravity"
)

func TestChatUpstreamErrorIsSanitized(t *testing.T) {
	w := httptest.NewRecorder()
	writeChatUpstreamError(w, &antigravity.UpstreamError{StatusCode: 400, Body: []byte(`{"error":{"message":"Function call is missing a thought_signature secret-prompt"}}`)})
	if w.Code != 400 || !strings.Contains(w.Body.String(), "thought_signature") || strings.Contains(w.Body.String(), "secret-prompt") {
		t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
	}
}
