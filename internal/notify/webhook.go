package notify

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/template"
	"time"
)

// WebhookNotifier sends notifications by POSTing to a URL.
// The request body is generated from a Go template with the Event fields.
type WebhookNotifier struct {
	URL      string
	Method   string // HTTP method (default "POST")
	Template string // Go text/template with .Status, .Message, .Config
	Client   *http.Client
}

// NewWebhookNotifier creates a notifier that sends HTTP requests to the given URL.
// If method is empty, it defaults to POST.
// If tmpl is empty, a default JSON template is used.
func NewWebhookNotifier(url, method, tmpl string) *WebhookNotifier {
	if method == "" {
		method = "POST"
	}
	if tmpl == "" {
		tmpl = `{"status": "{{.Status}}", "message": "{{.Message}}", "config": "{{.Config}}"}`
	}
	return &WebhookNotifier{
		URL:      url,
		Method:   method,
		Template: tmpl,
		Client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Send renders the template with event data and sends an HTTP request.
func (n *WebhookNotifier) Send(ctx context.Context, event Event) error {
	if n.URL == "" {
		return fmt.Errorf("notify.WebhookNotifier.Send: url is empty")
	}

	body, err := n.renderTemplate(event)
	if err != nil {
		return fmt.Errorf("notify.WebhookNotifier.Send: template render: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, n.Method, n.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify.WebhookNotifier.Send: create request: %w", err)
	}

	// Set content type based on whether the body looks like JSON.
	trimmed := strings.TrimSpace(string(body))
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		req.Header.Set("Content-Type", "application/json")
	} else {
		req.Header.Set("Content-Type", "text/plain")
	}
	req.Header.Set("User-Agent", "doomsday-notify/1.0")

	client := n.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("notify.WebhookNotifier.Send: %w", err)
	}
	defer resp.Body.Close()

	// Drain the body to allow connection reuse.
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("notify.WebhookNotifier.Send: HTTP %d from %s", resp.StatusCode, n.URL)
	}

	return nil
}

// renderTemplate executes the Go template with the event data.
func (n *WebhookNotifier) renderTemplate(event Event) ([]byte, error) {
	tmpl, err := template.New("webhook").Parse(n.Template)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, event); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}

	return buf.Bytes(), nil
}
