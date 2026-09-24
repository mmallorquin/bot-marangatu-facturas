// Package openrouter lee facturas usando cualquier modelo con visión disponible en OpenRouter.
package openrouter

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/reader"
)

const (
	DefaultBaseURL = "https://openrouter.ai/api/v1"

	requestTimeout  = 90 * time.Second
	maxResponseSize = 1 << 20 // 1 MB

	// Identifican la app en el panel de OpenRouter.
	appURL   = "https://github.com/mmallorquin/bot-marangatu-facturas"
	appTitle = "bot-marangatu-facturas"

	userInstruction = "Extraé los datos de este comprobante."
)

var (
	//go:embed prompt.md
	systemPrompt string

	//go:embed schema.json
	invoiceSchema json.RawMessage

	ErrNoChoices = errors.New("OpenRouter no devolvió ninguna respuesta")
	ErrTruncated = errors.New("la respuesta del modelo quedó cortada")
)

// APIError es un error devuelto por OpenRouter (key inválida, sin créditos, etc.).
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("OpenRouter respondió %d: %s", e.StatusCode, e.Message)
}

// Options configura el cliente.
type Options struct {
	APIKey  string
	Model   string // ej. "deepseek/deepseek-v4.1-flash"
	BaseURL string // vacío = DefaultBaseURL
	ZDR     bool   // usar solo proveedores que no guardan datos
}

// Client implementa reader.Reader con la API de OpenRouter.
type Client struct {
	opts Options
	http *http.Client
}

var _ reader.Reader = (*Client)(nil)

// New crea un cliente. Si BaseURL está vacío usa la API pública.
func New(opts Options) *Client {
	if opts.BaseURL == "" {
		opts.BaseURL = DefaultBaseURL
	}
	return &Client{opts: opts, http: &http.Client{Timeout: requestTimeout}}
}

// Read envía la imagen al modelo y devuelve la factura que leyó.
func (c *Client) Read(ctx context.Context, img reader.Image) (reader.Result, error) {
	payload, err := json.Marshal(c.buildRequest(img))
	if err != nil {
		return reader.Result{}, fmt.Errorf("armando el pedido: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.opts.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return reader.Result{}, fmt.Errorf("armando el pedido: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.opts.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", appURL)
	req.Header.Set("X-Title", appTitle)

	resp, err := c.http.Do(req)
	if err != nil {
		return reader.Result{}, fmt.Errorf("llamando a OpenRouter: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return reader.Result{}, fmt.Errorf("leyendo la respuesta: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return reader.Result{}, parseAPIError(resp.StatusCode, body)
	}
	return parseResult(body)
}

func (c *Client) buildRequest(img reader.Image) chatRequest {
	dataURL := "data:" + img.MimeType + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
	return chatRequest{
		Model: c.opts.Model,
		Messages: []message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: []contentPart{
				{Type: "text", Text: userInstruction},
				{Type: "image_url", ImageURL: &imageURL{URL: dataURL}},
			}},
		},
		ResponseFormat: responseFormat{
			Type:       "json_schema",
			JSONSchema: jsonSchema{Name: "comprobante", Strict: true, Schema: invoiceSchema},
		},
		Provider: providerPrefs{DataCollection: "deny", ZDR: c.opts.ZDR, RequireParameters: true},
	}
}

func parseAPIError(status int, body []byte) error {
	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	message := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &parsed) == nil && parsed.Error.Message != "" {
		message = parsed.Error.Message
	}
	return &APIError{StatusCode: status, Message: message}
}

func parseResult(body []byte) (reader.Result, error) {
	var resp chatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return reader.Result{}, fmt.Errorf("respuesta de OpenRouter inválida: %w", err)
	}
	if len(resp.Choices) == 0 {
		return reader.Result{}, ErrNoChoices
	}

	choice := resp.Choices[0]
	if choice.FinishReason == "length" {
		return reader.Result{}, ErrTruncated
	}

	var inv invoice.Invoice
	if err := json.Unmarshal([]byte(stripCodeFence(choice.Message.Content)), &inv); err != nil {
		return reader.Result{}, fmt.Errorf("el modelo no devolvió un JSON válido: %w", err)
	}
	return reader.Result{Invoice: inv, Model: resp.Model, CostUSD: resp.Usage.Cost}, nil
}

// stripCodeFence quita ```json ... ``` si el modelo envolvió la respuesta.
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	return strings.TrimSpace(strings.TrimSuffix(s, "```"))
}
