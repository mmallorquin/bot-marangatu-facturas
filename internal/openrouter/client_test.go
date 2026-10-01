package openrouter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/reader"
)

const invoiceJSON = `{"es_comprobante":true,"tipo":"factura","ruc_emisor":"80000519-8",` +
	`"razon_social_emisor":"Comercial Ejemplo S.A.","timbrado":"12345678","numero":"001-001-0001234",` +
	`"fecha":"2026-09-20","condicion":"contado","moneda":"PYG","exentas":0,"gravada_5":0,` +
	`"gravada_10":150000,"iva_5":0,"iva_10":13636,"total":150000,"cdc":"","campos_dudosos":[]}`

// fakeChatResponse arma una respuesta de OpenRouter con el contenido dado.
func fakeChatResponse(content, finishReason string) string {
	body, _ := json.Marshal(map[string]any{
		"model": "deepseek/deepseek-v4.1-flash",
		"choices": []map[string]any{{
			"message":       map[string]string{"role": "assistant", "content": content},
			"finish_reason": finishReason,
		}},
		"usage": map[string]any{
			"prompt_tokens": 1500, "completion_tokens": 200, "cost": 0.00042,
			"completion_tokens_details": map[string]any{"reasoning_tokens": 120},
		},
	})
	return string(body)
}

// newTestClient levanta un OpenRouter falso que responde con status y body.
// Guarda el último request recibido en *captured.
func newTestClient(t *testing.T, status int, body string, captured *capturedRequest, modify ...func(*Options)) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if captured != nil {
			captured.path = r.URL.Path
			captured.auth = r.Header.Get("Authorization")
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &captured.body)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)

	opts := Options{
		APIKey:  "sk-or-test",
		Model:   "deepseek/deepseek-v4.1-flash",
		BaseURL: server.URL,
		ZDR:     true,
	}
	for _, m := range modify {
		m(&opts)
	}
	return New(opts)
}

type capturedRequest struct {
	path string
	auth string
	body map[string]any
}

var testImage = reader.Image{Data: []byte("fake-jpeg"), MimeType: "image/jpeg"}

func TestReadParsesInvoiceAndCost(t *testing.T) {
	// Arrange
	client := newTestClient(t, http.StatusOK, fakeChatResponse(invoiceJSON, "stop"), nil)

	// Act
	result, err := client.Read(context.Background(), testImage)

	// Assert
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if result.Invoice.IssuerRUC != "80000519-8" || result.Invoice.Total != 150_000 {
		t.Errorf("factura mal parseada: %+v", result.Invoice)
	}
	if result.CostUSD != 0.00042 || result.Model != "deepseek/deepseek-v4.1-flash" {
		t.Errorf("costo o modelo incorrectos: %+v", result)
	}
	want := reader.Usage{InputTokens: 1500, OutputTokens: 200, ReasoningTokens: 120}
	if result.Usage != want {
		t.Errorf("uso de tokens = %+v, se esperaba %+v", result.Usage, want)
	}
}

func TestReadSendsReasoningEffortOnlyWhenConfigured(t *testing.T) {
	cases := map[string]struct {
		effort string
		want   map[string]any // nil = no se envía "reasoning"
	}{
		"sin configurar": {"", nil},
		"sin razonar":    {"none", map[string]any{"effort": "none", "exclude": true}},
		"bajo":           {"low", map[string]any{"effort": "low", "exclude": true}},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// Arrange
			var captured capturedRequest
			client := newTestClient(t, http.StatusOK, fakeChatResponse(invoiceJSON, "stop"), &captured,
				func(o *Options) { o.ReasoningEffort = tc.effort })

			// Act
			if _, err := client.Read(context.Background(), testImage); err != nil {
				t.Fatalf("error inesperado: %v", err)
			}

			// Assert
			got, sent := captured.body["reasoning"]
			if tc.want == nil {
				if sent {
					t.Errorf("no se esperaba \"reasoning\", se envió %v", got)
				}
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("reasoning = %v, se esperaba %v", got, tc.want)
			}
		})
	}
}

func TestReadSendsImagePrivacyAndSchema(t *testing.T) {
	// Arrange
	var captured capturedRequest
	client := newTestClient(t, http.StatusOK, fakeChatResponse(invoiceJSON, "stop"), &captured)

	// Act
	if _, err := client.Read(context.Background(), testImage); err != nil {
		t.Fatalf("error inesperado: %v", err)
	}

	// Assert
	if captured.path != "/chat/completions" {
		t.Errorf("path = %q", captured.path)
	}
	if captured.auth != "Bearer sk-or-test" {
		t.Errorf("Authorization = %q", captured.auth)
	}
	provider := captured.body["provider"].(map[string]any)
	if provider["data_collection"] != "deny" || provider["zdr"] != true || provider["require_parameters"] != true {
		t.Errorf("preferencias de privacidad incorrectas: %v", provider)
	}
	format := captured.body["response_format"].(map[string]any)
	if format["type"] != "json_schema" {
		t.Errorf("response_format = %v", format)
	}
	wantImage := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(testImage.Data)
	if raw, _ := json.Marshal(captured.body["messages"]); !strings.Contains(string(raw), wantImage) {
		t.Errorf("la imagen no se envió como data URL")
	}
}

func TestReadAcceptsJSONWrappedInCodeFence(t *testing.T) {
	client := newTestClient(t, http.StatusOK, fakeChatResponse("```json\n"+invoiceJSON+"\n```", "stop"), nil)

	result, err := client.Read(context.Background(), testImage)

	if err != nil || result.Invoice.Timbrado != "12345678" {
		t.Errorf("no se pudo leer JSON con ```: %v, %+v", err, result.Invoice)
	}
}

func TestReadReturnsAPIErrorWithStatus(t *testing.T) {
	// Arrange
	body := `{"error":{"code":402,"message":"Insufficient credits"}}`
	client := newTestClient(t, http.StatusPaymentRequired, body, nil)

	// Act
	_, err := client.Read(context.Background(), testImage)

	// Assert
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("se esperaba *APIError, se obtuvo %v", err)
	}
	if apiErr.StatusCode != http.StatusPaymentRequired {
		t.Errorf("APIError = %+v", apiErr)
	}
}

func TestReadClassifiesAPIFailuresWithoutExposingProviderDetails(t *testing.T) {
	// Una clasificación por HTTP solamente sugeriría reintentar una falta de saldo o credenciales.
	const secret = "sk-or-private-api-key ruc:80000001-1 provider invoice data"
	cases := []struct {
		name      string
		status    int
		body      string
		retryable bool
		hint      string
	}{
		{"credits", 402, `{"error":{"code":402,"message":"` + secret + `"}}`, false, "saldo"},
		{"credentials", 401, `{"error":{"code":401,"message":"` + secret + `"}}`, false, "configuración"},
		{"rate limit", 429, `{"error":{"code":429,"message":"` + secret + `"}}`, true, "minutos"},
		{"plain proxy failure", 503, "<html>" + secret + "</html>", true, "minutos"},
		{"embedded credits overrides status", 200, `{"error":{"code":500,"message":"` + secret + `","metadata":{"error_type":"payment_required","raw":"` + secret + `"}}}`, false, "saldo"},
		{"embedded authentication overrides status", 200, `{"choices":[{"message":{"role":"assistant","content":"partial"},"finish_reason":"error","error":{"code":502,"message":"` + secret + `","metadata":{"error_type":"authentication"}}}]}`, false, "configuración"},
		{"string error code", 200, `{"error":{"code":"rate_limit_exceeded","message":"` + secret + `"}}`, true, "minutos"},
		{"unreadable image", 400, `{"error":{"code":400,"message":"` + secret + `","metadata":{"error_type":"invalid_image"}}}`, false, "foto"},
		{"missing model", 404, `{"error":{"code":404,"message":"` + secret + `"}}`, false, "configuración"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, tc.status, tc.body, nil)
			_, err := client.Read(context.Background(), testImage)
			var classified reader.ClassifiedError
			if !errors.As(err, &classified) {
				t.Fatalf("failure cannot guide the next user action: %v", err)
			}
			if classified.Retryable() != tc.retryable {
				t.Errorf("Retryable() = %t, want %t", classified.Retryable(), tc.retryable)
			}
			if !strings.Contains(classified.UserMessage(), tc.hint) {
				t.Errorf("user cannot identify next action from %q; want hint %q", classified.UserMessage(), tc.hint)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("wanted APIError; got %T", err)
			}
			for _, exposed := range []string{err.Error(), classified.UserMessage(), apiErr.Message} {
				if strings.Contains(exposed, secret) || strings.Contains(exposed, "<html>") || strings.Contains(exposed, "\"error\"") || strings.Contains(exposed, "partial") {
					t.Errorf("provider response leaked: %q", exposed)
				}
			}
		})
	}
}

func TestReadClassifiesTransportFailureWithoutExposingRequestURL(t *testing.T) {
	// Los errores de net/http incluyen la URL: debe perderse su query y cualquier credencial.
	const secret = "sk-or-secret-in-url"
	client := newTestClient(t, http.StatusOK, fakeChatResponse(invoiceJSON, "stop"), nil)
	client.opts.BaseURL += "/?api_key=" + secret
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.Read(ctx, testImage)
	var classified reader.ClassifiedError
	if !errors.As(err, &classified) || classified.Kind() != reader.ErrorTransient || !classified.Retryable() {
		t.Fatalf("transport failure must allow a later manual retry: %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("context cancellation was lost: %v", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "api_key") {
		t.Errorf("request credentials leaked: %v", err)
	}
}

func TestReadClassifiesInvalidBaseURLWithoutExposingCredentials(t *testing.T) {
	client := New(Options{APIKey: "sk-or-key", Model: "test/model", BaseURL: "://sk-or-secret"})
	_, err := client.Read(context.Background(), testImage)
	var classified reader.ClassifiedError
	if !errors.As(err, &classified) || classified.Kind() != reader.ErrorConfiguration || classified.Retryable() {
		t.Fatalf("invalid configuration should ask the administrator to fix it: %v", err)
	}
	if strings.Contains(err.Error(), "sk-or-secret") {
		t.Errorf("request configuration leaked: %v", err)
	}
}

func TestReadClassifiesUnusableModelResponses(t *testing.T) {
	// Reintentar el mismo límite de salida no lo arregla; tampoco debe exponerse contenido del proveedor.
	cases := []struct {
		name     string
		body     string
		sentinel error
	}{
		{"no choices", `{"choices":[]}`, ErrNoChoices},
		{"malformed envelope", `{"sk-or-private": "provider data"`, nil},
		{"invalid content", fakeChatResponse(`{"total":"sk-or-private-invoice"}`, "stop"), nil},
		{"truncated answer", fakeChatResponse(invoiceJSON, "length"), ErrTruncated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.StatusOK, tc.body, nil)
			_, err := client.Read(context.Background(), testImage)
			var classified reader.ClassifiedError
			if !errors.As(err, &classified) || classified.Kind() != reader.ErrorResponse || classified.Retryable() {
				t.Fatalf("unusable model answer must guide review, without promising identical retries: %v", err)
			}
			if tc.sentinel != nil && !errors.Is(err, tc.sentinel) {
				t.Errorf("lost sentinel %v: %v", tc.sentinel, err)
			}
			if strings.Contains(err.Error(), "sk-or-private") || strings.Contains(classified.UserMessage(), "provider data") {
				t.Errorf("model response leaked: %v", err)
			}
		})
	}
}

func TestReadRejectsErrorsEmbeddedInSuccessfulHTTPResponse(t *testing.T) {
	// Si se ignora un error junto a contenido válido, se guardaría una factura incompleta.
	partial := fakeChatResponse(invoiceJSON, "error")
	var response map[string]any
	if err := json.Unmarshal([]byte(partial), &response); err != nil {
		t.Fatal(err)
	}
	response["choices"].([]any)[0].(map[string]any)["error"] = map[string]any{
		"code": 502, "message": "upstream private details",
		"metadata": map[string]any{"error_type": "provider_unavailable"},
	}
	choiceError, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		body   string
		status int
	}{
		{"root error", `{"error":{"code":402,"message":"Insufficient credits","metadata":{"error_type":"payment_required"}}}`, 402},
		{"choice error with valid partial JSON", string(choiceError), 502},
		{"error finish reason without details", partial, 502},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.StatusOK, tc.body, nil)
			result, err := client.Read(context.Background(), testImage)
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("wanted APIError for HTTP 200 failure; got result %+v, error %v", result, err)
			}
			if apiErr.StatusCode != tc.status {
				t.Errorf("status = %d, want %d", apiErr.StatusCode, tc.status)
			}
			if result.Invoice.IsInvoice || result.CostUSD != 0 {
				t.Errorf("failed response returned usable invoice or cost: %+v", result)
			}
		})
	}
}

func TestReadFailsOnUnusableResponses(t *testing.T) {
	cases := map[string]string{
		"sin choices":     `{"choices":[]}`,
		"JSON inválido":   fakeChatResponse("esto no es JSON", "stop"),
		"respuesta corta": fakeChatResponse(`{"es_comprobante":true`, "length"),
		"body roto":       `{`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			client := newTestClient(t, http.StatusOK, body, nil)

			_, err := client.Read(context.Background(), testImage)

			if err == nil {
				t.Error("se esperaba un error")
			}
		})
	}
}

func TestSchemaListsEveryInvoiceField(t *testing.T) {
	// El esquema que ve el modelo debe tener exactamente los campos de invoice.Invoice.
	var schema struct {
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	if err := json.Unmarshal(invoiceSchema, &schema); err != nil {
		t.Fatalf("schema.json inválido: %v", err)
	}

	var asMap map[string]any
	raw, _ := json.Marshal(invoice.Invoice{})
	_ = json.Unmarshal(raw, &asMap)

	for field := range asMap {
		if _, ok := schema.Properties[field]; !ok {
			t.Errorf("falta %q en schema.json", field)
		}
	}
	if len(schema.Properties) != len(asMap) || len(schema.Required) != len(asMap) {
		t.Errorf("schema tiene %d propiedades y %d requeridas; Invoice tiene %d campos",
			len(schema.Properties), len(schema.Required), len(asMap))
	}
}

func TestReadSendsPDFAsFile(t *testing.T) {
	// Arrange
	var captured capturedRequest
	client := newTestClient(t, http.StatusOK, fakeChatResponse(invoiceJSON, "stop"), &captured)
	pdf := reader.Image{Data: []byte("%PDF-1.7 factura"), MimeType: "application/pdf"}

	// Act
	if _, err := client.Read(context.Background(), pdf); err != nil {
		t.Fatalf("error inesperado: %v", err)
	}

	// Assert
	raw, _ := json.Marshal(captured.body["messages"])
	wantFile := `{"file":{"file_data":"data:application/pdf;base64,` + base64.StdEncoding.EncodeToString(pdf.Data) +
		`","filename":"factura.pdf"},"type":"file"}`
	if !strings.Contains(string(raw), wantFile) {
		t.Errorf("el PDF no se envió como archivo:\n%s", raw)
	}
	if strings.Contains(string(raw), "image_url") {
		t.Errorf("un PDF no debería ir como imagen:\n%s", raw)
	}
}

func TestReadRequiresNativePDFExtractionWithoutProviderFallback(t *testing.T) {
	// Omitir el motor puede activar OCR con costo extra; omitir allow_fallbacks puede enviar a otro proveedor.
	var captured capturedRequest
	client := newTestClient(t, http.StatusOK, fakeChatResponse(invoiceJSON, "stop"), &captured)
	pdf := reader.Image{Data: []byte("%PDF-1.7 factura"), MimeType: "application/pdf"}
	if _, err := client.Read(context.Background(), pdf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantPlugins := []any{map[string]any{
		"id": "file-parser", "pdf": map[string]any{"engine": "native"},
	}}
	if !reflect.DeepEqual(captured.body["plugins"], wantPlugins) {
		t.Errorf("PDF may use implicit paid extraction: plugins = %v, want %v", captured.body["plugins"], wantPlugins)
	}
	provider := captured.body["provider"].(map[string]any)
	if provider["allow_fallbacks"] != false {
		t.Errorf("PDF may retry another provider: allow_fallbacks = %v, want false", provider["allow_fallbacks"])
	}
	if provider["data_collection"] != "deny" || provider["zdr"] != true || provider["require_parameters"] != true {
		t.Errorf("PDF privacy preferences were lost: %v", provider)
	}
	if captured.body["model"] != "deepseek/deepseek-v4.1-flash" || captured.body["models"] != nil {
		t.Errorf("PDF must use only the explicitly configured model: %v", captured.body["model"])
	}
}

func TestReadKeepsImageRoutingFreeOfPDFParser(t *testing.T) {
	var captured capturedRequest
	client := newTestClient(t, http.StatusOK, fakeChatResponse(invoiceJSON, "stop"), &captured)
	if _, err := client.Read(context.Background(), testImage); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, present := captured.body["plugins"]; present {
		t.Errorf("image unexpectedly uses a document parser: %v", captured.body["plugins"])
	}
	provider := captured.body["provider"].(map[string]any)
	if _, present := provider["allow_fallbacks"]; present {
		t.Errorf("image provider routing changed: %v", provider)
	}
}
