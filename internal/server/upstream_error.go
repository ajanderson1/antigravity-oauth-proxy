package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/dvcrn/antigravity-oauth-proxy/internal/antigravity"
)

// Preserve upstream status without echoing bodies that may contain private input.
func writeChatUpstreamError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	message := "Upstream generation request failed"
	var upstream *antigravity.UpstreamError
	if errors.As(err, &upstream) {
		if upstream.StatusCode >= 400 && upstream.StatusCode <= 599 {
			status = upstream.StatusCode
		}
		if status == http.StatusBadRequest && strings.Contains(string(upstream.Body), "thought_signature") {
			message = "Upstream rejected tool history: missing or invalid thought_signature"
		}
	}
	http.Error(w, message, status)
}
