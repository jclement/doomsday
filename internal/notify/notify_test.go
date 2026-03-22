package notify

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- Event + Policy tests ---

func TestShouldNotify(t *testing.T) {
	tests := []struct {
		name   string
		policy Policy
		status string
		want   bool
	}{
		{"always/success", PolicyAlways, "success", true},
		{"always/failure", PolicyAlways, "failure", true},
		{"always/warning", PolicyAlways, "warning", true},
		{"on_failure/success", PolicyOnFailure, "success", false},
		{"on_failure/failure", PolicyOnFailure, "failure", true},
		{"on_failure/warning", PolicyOnFailure, "warning", true},
		{"never/success", PolicyNever, "success", false},
		{"never/failure", PolicyNever, "failure", false},
		{"never/warning", PolicyNever, "warning", false},
		{"unknown/failure", Policy("bogus"), "failure", true},
		{"unknown/success", Policy("bogus"), "success", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := Event{Status: tt.status, Message: "test", Config: "home"}
			got := ShouldNotify(tt.policy, event)
			if got != tt.want {
				t.Errorf("ShouldNotify(%q, %q) = %v, want %v", tt.policy, tt.status, got, tt.want)
			}
		})
	}
}

// --- CommandNotifier tests ---

func TestCommandNotifier_Send_Success(t *testing.T) {
	n := NewCommandNotifier("true")
	err := n.Send(context.Background(), Event{
		Status:  "success",
		Message: "Backup completed",
		Config:  "home",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func TestCommandNotifier_Send_Failure(t *testing.T) {
	n := NewCommandNotifier("false")
	err := n.Send(context.Background(), Event{
		Status:  "failure",
		Message: "Backup failed",
		Config:  "home",
	})
	if err == nil {
		t.Error("expected error when command fails")
	}
}

func TestCommandNotifier_Send_EmptyCommand(t *testing.T) {
	n := NewCommandNotifier("")
	err := n.Send(context.Background(), Event{Status: "failure"})
	if err == nil {
		t.Error("expected error for empty command")
	}
}

func TestCommandNotifier_Send_EnvironmentVars(t *testing.T) {
	// Use a command that checks for our env vars.
	n := NewCommandNotifier(`test "$DOOMSDAY_STATUS" = "failure" && test "$DOOMSDAY_CONFIG" = "projects"`)
	err := n.Send(context.Background(), Event{
		Status:  "failure",
		Message: "Connection lost",
		Config:  "projects",
	})
	if err != nil {
		t.Fatalf("Send with env var check: %v", err)
	}
}

func TestCommandNotifier_Send_NoTemplateExpansion(t *testing.T) {
	// Template vars must NOT be expanded in the command string (security: prevents injection).
	// Event data is available via environment variables instead.
	n := NewCommandNotifier(`test "$DOOMSDAY_STATUS" = "warning"`)
	err := n.Send(context.Background(), Event{
		Status:  "warning",
		Message: "Stale lock",
		Config:  "home",
	})
	if err != nil {
		t.Fatalf("Send with env var access: %v", err)
	}
}

func TestCommandNotifier_Send_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	n := NewCommandNotifier("sleep 10")
	err := n.Send(ctx, Event{Status: "failure"})
	if err == nil {
		t.Error("expected error when context is cancelled")
	}
}

// --- WebhookNotifier tests ---

func TestWebhookNotifier_Send_Success(t *testing.T) {
	var gotBody string
	var gotContentType string
	var gotMethod string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	n := NewWebhookNotifier(server.URL, "POST", `{"text": "{{.Status}}: {{.Message}}"}`)
	err := n.Send(context.Background(), Event{
		Status:  "failure",
		Message: "NAS unreachable",
		Config:  "home",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if gotMethod != "POST" {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Errorf("content-type = %q, want application/json", gotContentType)
	}
	if !strings.Contains(gotBody, "failure") || !strings.Contains(gotBody, "NAS unreachable") {
		t.Errorf("body = %q, expected failure + NAS unreachable", gotBody)
	}
}

func TestWebhookNotifier_Send_PlainTextTemplate(t *testing.T) {
	var gotContentType string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	n := NewWebhookNotifier(server.URL, "POST", "Backup {{.Config}} {{.Status}}")
	err := n.Send(context.Background(), Event{
		Status:  "success",
		Message: "Done",
		Config:  "home",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotContentType != "text/plain" {
		t.Errorf("content-type = %q, want text/plain", gotContentType)
	}
}

func TestWebhookNotifier_Send_DefaultTemplate(t *testing.T) {
	var gotBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	n := NewWebhookNotifier(server.URL, "", "")
	err := n.Send(context.Background(), Event{
		Status:  "success",
		Message: "All good",
		Config:  "projects",
	})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(gotBody, `"status": "success"`) {
		t.Errorf("default template body = %q", gotBody)
	}
	if !strings.Contains(gotBody, `"config": "projects"`) {
		t.Errorf("default template body = %q", gotBody)
	}
}

func TestWebhookNotifier_Send_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	n := NewWebhookNotifier(server.URL, "POST", "")
	err := n.Send(context.Background(), Event{Status: "failure"})
	if err == nil {
		t.Error("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention status code: %v", err)
	}
}

func TestWebhookNotifier_Send_EmptyURL(t *testing.T) {
	n := NewWebhookNotifier("", "POST", "")
	err := n.Send(context.Background(), Event{Status: "failure"})
	if err == nil {
		t.Error("expected error for empty URL")
	}
}

func TestWebhookNotifier_Send_InvalidTemplate(t *testing.T) {
	n := NewWebhookNotifier("http://example.com", "POST", "{{.Invalid")
	err := n.Send(context.Background(), Event{Status: "failure"})
	if err == nil {
		t.Error("expected error for invalid template")
	}
}

func TestWebhookNotifier_Send_CustomMethod(t *testing.T) {
	var gotMethod string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	n := NewWebhookNotifier(server.URL, "PUT", "")
	err := n.Send(context.Background(), Event{Status: "success"})
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != "PUT" {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
}

func TestWebhookNotifier_Send_ContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Slow server -- will be cancelled.
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	n := NewWebhookNotifier(server.URL, "POST", "")
	err := n.Send(ctx, Event{Status: "failure"})
	if err == nil {
		t.Error("expected error when context is cancelled")
	}
}

func TestWebhookNotifier_Send_UserAgent(t *testing.T) {
	var gotUA string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	n := NewWebhookNotifier(server.URL, "POST", "")
	_ = n.Send(context.Background(), Event{Status: "success"})

	if gotUA != "doomsday-notify/1.0" {
		t.Errorf("User-Agent = %q, want doomsday-notify/1.0", gotUA)
	}
}

// --- Multi notifier tests ---

func TestMulti_Send_AllSucceed(t *testing.T) {
	n1 := &mockNotifier{}
	n2 := &mockNotifier{}
	multi := &Multi{Notifiers: []Notifier{n1, n2}}

	event := Event{Status: "success", Message: "test", Config: "home"}
	err := multi.Send(context.Background(), event)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !n1.called || !n2.called {
		t.Error("expected both notifiers to be called")
	}
}

func TestMulti_Send_OneFails(t *testing.T) {
	n1 := &mockNotifier{}
	n2 := &mockNotifier{err: fmt.Errorf("webhook down")}
	n3 := &mockNotifier{}
	multi := &Multi{Notifiers: []Notifier{n1, n2, n3}}

	event := Event{Status: "failure", Message: "test", Config: "home"}
	err := multi.Send(context.Background(), event)
	if err == nil {
		t.Error("expected error when one notifier fails")
	}

	// All notifiers should still be attempted.
	if !n1.called || !n2.called || !n3.called {
		t.Error("expected all notifiers to be attempted even on failure")
	}
}

func TestMulti_Send_AllFail(t *testing.T) {
	n1 := &mockNotifier{err: fmt.Errorf("fail 1")}
	n2 := &mockNotifier{err: fmt.Errorf("fail 2")}
	multi := &Multi{Notifiers: []Notifier{n1, n2}}

	err := multi.Send(context.Background(), Event{Status: "failure"})
	if err == nil {
		t.Error("expected error when all notifiers fail")
	}
	if !strings.Contains(err.Error(), "2 notifiers failed") {
		t.Errorf("error should mention count: %v", err)
	}
}

func TestMulti_Send_Empty(t *testing.T) {
	multi := &Multi{}
	err := multi.Send(context.Background(), Event{Status: "success"})
	if err != nil {
		t.Fatalf("Send with no notifiers: %v", err)
	}
}

// mockNotifier is a test double for the Notifier interface.
type mockNotifier struct {
	called bool
	event  Event
	err    error
}

func (m *mockNotifier) Send(_ context.Context, event Event) error {
	m.called = true
	m.event = event
	return m.err
}
