package parser

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

type InvoiceData struct {
	InvoiceNumber string        `json:"invoice_number"`
	InvoiceDate   string        `json:"invoice_date"`
	DueDate       string        `json:"due_date"`
	Currency      string        `json:"currency"`
	Vendor        VendorInfo    `json:"vendor"`
	Customer      CustomerInfo  `json:"customer"`
	LineItems     []LineItem    `json:"line_items"`
	Subtotal      float64       `json:"subtotal"`
	TaxRate       float64       `json:"tax_rate"`
	TaxAmount     float64       `json:"tax_amount"`
	Discount      float64       `json:"discount"`
	Total         float64       `json:"total"`
	PaymentTerms  string        `json:"payment_terms"`
	Notes         string        `json:"notes"`
	Confidence    float64       `json:"confidence"`
	RawFields     []ParsedField `json:"raw_fields,omitempty"`
}

type VendorInfo struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Email   string `json:"email"`
	Phone   string `json:"phone"`
	TaxID   string `json:"tax_id"`
}

type CustomerInfo struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Email   string `json:"email"`
	Phone   string `json:"phone"`
}

type LineItem struct {
	Description string  `json:"description"`
	Quantity    float64 `json:"quantity"`
	UnitPrice   float64 `json:"unit_price"`
	Amount      float64 `json:"amount"`
}

type ParsedField struct {
	Field      string  `json:"field"`
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence"`
}

type Parser struct {
	apiKey string
	apiURL string
	model  string
	client *http.Client
}

func New(apiKey string) *Parser {
	return &Parser{
		apiKey: apiKey,
		apiURL: "https://api.groq.com/openai/v1/chat/completions",
		model:  "meta-llama/llama-4-scout-17b-16e-instruct",
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

const systemPrompt = `You are a precise invoice data extraction engine. Extract all structured data from the provided invoice image/document.

Return ONLY valid JSON matching this exact schema (no markdown, no backticks, just raw JSON):
{
  "invoice_number": "string",
  "invoice_date": "YYYY-MM-DD",
  "due_date": "YYYY-MM-DD",
  "currency": "USD/EUR/GBP/etc",
  "vendor": {
    "name": "string",
    "address": "string",
    "email": "string",
    "phone": "string",
    "tax_id": "string"
  },
  "customer": {
    "name": "string",
    "address": "string",
    "email": "string",
    "phone": "string"
  },
  "line_items": [
    {
      "description": "string",
      "quantity": number,
      "unit_price": number,
      "amount": number
    }
  ],
  "subtotal": number,
  "tax_rate": number,
  "tax_amount": number,
  "discount": number,
  "total": number,
  "payment_terms": "string",
  "notes": "string",
  "confidence": number between 0 and 1
}

Rules:
- Use empty string "" for fields not found
- Use 0 for numeric fields not found
- Dates must be YYYY-MM-DD format
- All monetary values as numbers (no currency symbols)
- confidence: 1.0 if all key fields extracted, lower if some are missing/unclear
- If the image is not an invoice, return {"error": "not_an_invoice", "confidence": 0}`

const maxRetries = 3

// ParseBytes parses invoice data from raw bytes and a filename.
func (p *Parser) ParseBytes(data []byte, filename string) (*InvoiceData, error) {
	mediaType := detectMediaType(filename, "")
	return p.parseInvoiceData(data, mediaType)
}

func (p *Parser) Parse(file multipart.File, header *multipart.FileHeader) (*InvoiceData, error) {
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	mediaType := detectMediaType(header.Filename, header.Header.Get("Content-Type"))
	return p.parseInvoiceData(data, mediaType)
}

// ParseDocument parses any supported document type from a multipart file upload.
// Returns the raw JSON result — caller is responsible for unmarshaling to the right type.
func (p *Parser) ParseDocument(docType DocType, file multipart.File, header *multipart.FileHeader) (json.RawMessage, error) {
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	mediaType := detectMediaType(header.Filename, header.Header.Get("Content-Type"))
	return p.parseRaw(data, mediaType, docType)
}

// ParseDocumentBytes parses any document type from raw bytes.
func (p *Parser) ParseDocumentBytes(docType DocType, data []byte, filename string) (json.RawMessage, error) {
	mediaType := detectMediaType(filename, "")
	return p.parseRaw(data, mediaType, docType)
}

func (p *Parser) parseRaw(data []byte, mediaType string, docType DocType) (json.RawMessage, error) {
	prompt, ok := docTypePrompts[docType]
	if !ok {
		return nil, fmt.Errorf("unsupported document type: %s", docType)
	}
	return p.callAPI(data, mediaType, prompt)
}

func (p *Parser) parseInvoiceData(data []byte, mediaType string) (*InvoiceData, error) {
	raw, err := p.callAPI(data, mediaType, systemPrompt)
	if err != nil {
		return nil, err
	}
	var invoice InvoiceData
	if err := json.Unmarshal(raw, &invoice); err != nil {
		return nil, fmt.Errorf("parsing invoice data: %w (raw: %s)", err, string(raw))
	}
	return &invoice, nil
}

func (p *Parser) callAPI(data []byte, mediaType, prompt string) (json.RawMessage, error) {
	b64 := base64.StdEncoding.EncodeToString(data)

	// Groq uses the OpenAI-compatible chat completions format
	imageURL := fmt.Sprintf("data:%s;base64,%s", mediaType, b64)

	requestBody := map[string]interface{}{
		"model": p.model,
		"messages": []map[string]interface{}{
			{
				"role":    "system",
				"content": prompt,
			},
			{
				"role": "user",
				"content": []map[string]interface{}{
					{
						"type": "image_url",
						"image_url": map[string]interface{}{
							"url": imageURL,
						},
					},
					{
						"type": "text",
						"text": "Extract all data from this document. Return only JSON.",
					},
				},
			},
		},
		"temperature": 0.1,
		"max_tokens":  4096,
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	// Retry with exponential backoff
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			slog.Warn("retrying API call", "attempt", attempt+1, "backoff", backoff)
			time.Sleep(backoff)
		}

		result, err := p.doRequest(bodyBytes)
		if err == nil {
			return result, nil
		}
		lastErr = err

		if !isRetryable(err) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("API failed after %d attempts: %w", maxRetries, lastErr)
}

func (p *Parser) doRequest(bodyBytes []byte) (json.RawMessage, error) {
	req, err := http.NewRequest("POST", p.apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, &retryableError{fmt.Errorf("calling API: %w", err)}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &retryableError{fmt.Errorf("reading response: %w", err)}
	}

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, &retryableError{fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	// OpenAI-compatible response format
	var apiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("parsing API response: %w", err)
	}

	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("empty response from API")
	}

	text := apiResp.Choices[0].Message.Content
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)

	// Validate it's valid JSON before returning
	if !json.Valid([]byte(text)) {
		return nil, fmt.Errorf("invalid JSON from API (raw: %s)", text)
	}

	return json.RawMessage(text), nil
}

type retryableError struct{ error }

func isRetryable(err error) bool {
	_, ok := err.(*retryableError)
	return ok
}

func detectMediaType(filename, contentType string) string {
	if contentType != "" && contentType != "application/octet-stream" {
		return contentType
	}

	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".pdf"):
		return "application/pdf"
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(lower, ".webp"):
		return "image/webp"
	case strings.HasSuffix(lower, ".gif"):
		return "image/gif"
	default:
		return "image/png"
	}
}
