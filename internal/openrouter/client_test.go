package openrouter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
		"usage": map[string]any{"prompt_tokens": 1500, "completion_tokens": 200, "cost": 0.00042},
	})
	return string(body)
}

// newTestClient levanta un OpenRouter falso que responde con status y body.
// Guarda el último request recibido en *captured.
func newTestClient(t *testing.T, status int, body string, captured *capturedRequest) *Client {
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

	return New(Options{
		APIKey:  "sk-or-test",
		Model:   "deepseek/deepseek-v4.1-flash",
		BaseURL: server.URL,
		ZDR:     true,
	})
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

func TestReadReturnsAPIErrorWithStatusAndMessage(t *testing.T) {
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
	if apiErr.StatusCode != http.StatusPaymentRequired || apiErr.Message != "Insufficient credits" {
		t.Errorf("APIError = %+v", apiErr)
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
