package parser

import (
	"testing"
)

func TestDetectMediaType(t *testing.T) {
	tests := []struct {
		filename    string
		contentType string
		expected    string
	}{
		{"invoice.pdf", "", "application/pdf"},
		{"invoice.png", "", "image/png"},
		{"invoice.jpg", "", "image/jpeg"},
		{"invoice.jpeg", "", "image/jpeg"},
		{"invoice.webp", "", "image/webp"},
		{"invoice.gif", "", "image/gif"},
		{"INVOICE.PDF", "", "application/pdf"},
		{"unknown.xyz", "", "image/png"}, // default
		{"invoice.pdf", "application/pdf", "application/pdf"},
		{"invoice.pdf", "application/octet-stream", "application/pdf"}, // falls through to filename
		{"invoice.png", "image/png", "image/png"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := detectMediaType(tt.filename, tt.contentType)
			if got != tt.expected {
				t.Errorf("detectMediaType(%q, %q) = %q, want %q", tt.filename, tt.contentType, got, tt.expected)
			}
		})
	}
}

func TestNewParser(t *testing.T) {
	p := New("test-key")
	if p.apiKey != "test-key" {
		t.Errorf("expected apiKey 'test-key', got %q", p.apiKey)
	}
	if p.model != "claude-sonnet-4-20250514" {
		t.Errorf("unexpected model: %s", p.model)
	}
	if p.client == nil {
		t.Error("expected http client to be set")
	}
}

func TestIsRetryable(t *testing.T) {
	retryable := &retryableError{error: nil}
	if !isRetryable(retryable) {
		t.Error("expected retryableError to be retryable")
	}

	regular := &struct{ error }{error: nil}
	if isRetryable(regular) {
		t.Error("expected regular error to not be retryable")
	}
}
