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

	pdfMimeType = "application/pdf"
	pdfFileName = "factura.pdf"
)

var (
	//go:embed prompt.md
	systemPrompt string

	//go:embed schema.json
	invoiceSchema json.RawMessage

	ErrNoChoices = errors.New("OpenRouter no devolvió ninguna respuesta")
	ErrTruncated = errors.New("la respuesta del modelo quedó cortada")
)

// APIError clasifica una falla de lectura sin conservar respuestas ni credenciales.
type APIError struct {
	StatusCode int    // 0 si no hubo una respuesta HTTP utilizable
	Message    string // mensaje seguro; nunca contiene el texto o cuerpo del proveedor
	kind       reader.ErrorKind
	cause      error // solo sentinelas locales seguros, nunca el error original de red o JSON
}

func (e *APIError) Error() string {
	if e.StatusCode == 0 {
		return "OpenRouter: " + e.UserMessage()
	}
	return fmt.Sprintf("OpenRouter respondió %d: %s", e.StatusCode, e.UserMessage())
}

func (e *APIError) Unwrap() error { return e.cause }

var _ reader.ClassifiedError = (*APIError)(nil)

func (e *APIError) Kind() reader.ErrorKind {
	if e.kind != "" {
		return e.kind
	}
	return classifyStatus(e.StatusCode)
}

func (e *APIError) Retryable() bool { return e.Kind() == reader.ErrorTransient }

func (e *APIError) UserMessage() string {
	switch e.Kind() {
	case reader.ErrorTransient:
		return "El servicio de lectura está temporalmente ocupado o no responde. Esperá unos minutos y volvé a enviar el archivo."
	case reader.ErrorQuota:
		return "El servicio de lectura no tiene saldo disponible. Avisá al administrador para que revise el saldo; cuando esté resuelto, volvé a enviar el archivo."
	case reader.ErrorConfiguration:
		return "El servicio de lectura necesita revisar su configuración. Avisá al administrador; cuando esté resuelto, volvé a enviar el archivo."
	case reader.ErrorDocument:
		return "El servicio no pudo leer este archivo. Enviá una foto clara o un PDF más pequeño, con una sola factura por archivo."
	default:
		return "La respuesta de la lectura quedó incompleta o no se pudo interpretar. Enviá una foto clara de una sola factura; si vuelve a pasar, avisá al administrador."
	}
}

// Options configura el cliente.
type Options struct {
	APIKey  string
	Model   string // ej. "deepseek/deepseek-v4.1-flash"
	BaseURL string // vacío = DefaultBaseURL
	ZDR     bool   // usar solo proveedores que no guardan datos

	// ReasoningEffort limita el razonamiento del modelo (none, minimal, low, medium, high).
	// Vacío = no se envía y cada modelo usa su valor por defecto.
	ReasoningEffort string
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

// Read envía la imagen o el PDF al modelo y devuelve la factura que leyó.
func (c *Client) Read(ctx context.Context, img reader.Image) (reader.Result, error) {
	payload, err := json.Marshal(c.buildRequest(img))
	if err != nil {
		return reader.Result{}, newReadError(reader.ErrorConfiguration, nil)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.opts.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return reader.Result{}, newReadError(reader.ErrorConfiguration, nil)
	}
	req.Header.Set("Authorization", "Bearer "+c.opts.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", appURL)
	req.Header.Set("X-Title", appTitle)

	resp, err := c.http.Do(req)
	if err != nil {
		return reader.Result{}, newReadError(reader.ErrorTransient, contextCause(err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return reader.Result{}, newReadError(reader.ErrorTransient, contextCause(err))
	}
	if resp.StatusCode != http.StatusOK {
		return reader.Result{}, parseAPIError(resp.StatusCode, body)
	}
	return parseResult(body)
}

func (c *Client) buildRequest(img reader.Image) chatRequest {
	dataURL := "data:" + img.MimeType + ";base64," + base64.StdEncoding.EncodeToString(img.Data)

	var effort *reasoning
	if c.opts.ReasoningEffort != "" {
		effort = &reasoning{Effort: c.opts.ReasoningEffort, Exclude: true}
	}
	provider := providerPrefs{DataCollection: "deny", ZDR: c.opts.ZDR, RequireParameters: true}
	var plugins []plugin
	if img.MimeType == pdfMimeType {
		// "native" evita el OCR automático con costo adicional si el modelo no admite archivos.
		plugins = []plugin{{ID: "file-parser", PDF: pdfEngine{Engine: "native"}}}
		allowFallbacks := false
		provider.AllowFallbacks = &allowFallbacks
	}

	return chatRequest{
		Model: c.opts.Model,
		Messages: []message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: []contentPart{
				{Type: "text", Text: userInstruction},
				attachment(img.MimeType, dataURL),
			}},
		},
		ResponseFormat: responseFormat{
			Type:       "json_schema",
			JSONSchema: jsonSchema{Name: "comprobante", Strict: true, Schema: invoiceSchema},
		},
		Provider:  provider,
		Reasoning: effort,
		Plugins:   plugins,
	}
}

// attachment arma la parte del mensaje con el comprobante: un PDF va como archivo y una foto como imagen.
func attachment(mimeType, dataURL string) contentPart {
	if mimeType == pdfMimeType {
		return contentPart{Type: "file", File: &fileData{Filename: pdfFileName, FileData: dataURL}}
	}
	return contentPart{Type: "image_url", ImageURL: &imageURL{URL: dataURL}}
}

func parseAPIError(status int, body []byte) error {
	var parsed struct {
		Error *responseError `json:"error"`
	}
	// Incluso un cuerpo HTML o un error malformado queda reducido a una categoría segura.
	_ = json.Unmarshal(body, &parsed)
	return newAPIError(status, parsed.Error)
}

func parseResult(body []byte) (reader.Result, error) {
	var resp chatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return reader.Result{}, newReadError(reader.ErrorResponse, nil)
	}
	if resp.Error != nil {
		return reader.Result{}, embeddedAPIError(resp.Error)
	}
	if len(resp.Choices) == 0 {
		return reader.Result{}, newReadError(reader.ErrorResponse, ErrNoChoices)
	}

	choice := resp.Choices[0]
	if choice.Error != nil {
		return reader.Result{}, embeddedAPIError(choice.Error)
	}
	if choice.FinishReason == "error" {
		return reader.Result{}, newAPIError(http.StatusBadGateway, nil)
	}
	if choice.FinishReason == "length" {
		return reader.Result{}, newReadError(reader.ErrorResponse, ErrTruncated)
	}

	var inv invoice.Invoice
	content := []byte(stripCodeFence(choice.Message.Content))
	if err := validateInvoiceJSON(content); err != nil {
		return reader.Result{}, newReadError(reader.ErrorResponse, nil)
	}
	if err := json.Unmarshal(content, &inv); err != nil {
		return reader.Result{}, newReadError(reader.ErrorResponse, nil)
	}
	usage := reader.Usage{
		InputTokens:     resp.Usage.PromptTokens,
		OutputTokens:    resp.Usage.CompletionTokens,
		ReasoningTokens: resp.Usage.CompletionTokensDetails.ReasoningTokens,
	}
	return reader.Result{Invoice: inv, Model: resp.Model, CostUSD: resp.Usage.Cost, Usage: usage}, nil
}

// validateInvoiceJSON enforces the embedded response schema before decoding into
// the Go struct, whose zero values would otherwise hide omitted or null fields.
func validateInvoiceJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	var schema map[string]any
	if err := json.Unmarshal(invoiceSchema, &schema); err != nil {
		return err
	}
	return validateJSONSchema(value, schema)
}

func validateJSONSchema(value any, schema map[string]any) error {
	typeName, _ := schema["type"].(string)
	switch typeName {
	case "object":
		object, ok := value.(map[string]any)
		if !ok || object == nil {
			return errors.New("expected object")
		}
		properties, _ := schema["properties"].(map[string]any)
		for _, rawRequired := range schema["required"].([]any) {
			name := rawRequired.(string)
			if _, ok := object[name]; !ok {
				return errors.New("missing required property")
			}
		}
		for name, raw := range object {
			rawProperty, exists := properties[name]
			if !exists {
				if schema["additionalProperties"] == false {
					return errors.New("additional property")
				}
				continue
			}
			propertySchema, ok := rawProperty.(map[string]any)
			if !ok || validateJSONSchema(raw, propertySchema) != nil {
				return errors.New("invalid property")
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return errors.New("expected array")
		}
		itemSchema, ok := schema["items"].(map[string]any)
		if !ok {
			return errors.New("invalid array schema")
		}
		for _, item := range items {
			if err := validateJSONSchema(item, itemSchema); err != nil {
				return err
			}
		}
	case "string":
		if _, ok := value.(string); !ok {
			return errors.New("expected string")
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return errors.New("expected boolean")
		}
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return errors.New("expected integer")
		}
		if _, err := number.Int64(); err != nil {
			return errors.New("expected integer")
		}
	default:
		return errors.New("unsupported schema type")
	}
	if allowed, exists := schema["enum"].([]any); exists {
		matched := false
		for _, candidate := range allowed {
			if candidate == value {
				matched = true
				break
			}
		}
		if !matched {
			return errors.New("value outside enum")
		}
	}
	return nil
}

func newReadError(kind reader.ErrorKind, cause error) *APIError {
	apiError := &APIError{kind: kind, cause: cause}
	apiError.Message = apiError.UserMessage()
	return apiError
}

// contextCause conserva cancelación/deadline para errors.Is, sin retener URLs del error de net/http.
func contextCause(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func embeddedAPIError(providerError *responseError) error {
	status, _ := responseErrorCode(providerError.Code)
	if status < 400 || status > 599 {
		status = http.StatusBadGateway
	}
	return newAPIError(status, providerError)
}

func newAPIError(status int, details *responseError) *APIError {
	apiError := &APIError{StatusCode: status, kind: classifyStatus(status)}
	if details != nil {
		errorType := details.Metadata.ErrorType
		if errorType == "" {
			_, errorType = responseErrorCode(details.Code)
		}
		if kind := classifyErrorType(errorType); kind != "" {
			apiError.kind = kind
		}
	}
	apiError.Message = apiError.UserMessage()
	return apiError
}

func responseErrorCode(code json.RawMessage) (int, string) {
	var status int
	if json.Unmarshal(code, &status) == nil {
		return status, ""
	}
	var errorType string
	_ = json.Unmarshal(code, &errorType)
	return 0, errorType
}

func classifyStatus(status int) reader.ErrorKind {
	switch {
	case status == http.StatusPaymentRequired:
		return reader.ErrorQuota
	case status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500:
		return reader.ErrorTransient
	case status == http.StatusRequestEntityTooLarge || status == http.StatusUnprocessableEntity:
		return reader.ErrorDocument
	default:
		return reader.ErrorConfiguration
	}
}

func classifyErrorType(errorType string) reader.ErrorKind {
	switch errorType {
	case "payment_required", "insufficient_credits", "token_limit_exceeded":
		return reader.ErrorQuota
	case "rate_limit_exceeded", "provider_overloaded", "provider_unavailable", "server", "server_error", "timeout", "image_download_failed":
		return reader.ErrorTransient
	case "authentication", "permission_denied", "invalid_request", "invalid_prompt", "not_found", "precondition_failed", "content_policy_violation", "refusal":
		return reader.ErrorConfiguration
	case "context_length_exceeded", "string_too_long", "payload_too_large", "unprocessable", "invalid_image", "image_too_large", "image_too_small", "unsupported_image_format", "image_not_found":
		return reader.ErrorDocument
	case "max_tokens_exceeded":
		return reader.ErrorResponse
	default:
		return ""
	}
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
