package openrouter

import "encoding/json"

// Tipos del formato de la API de chat de OpenRouter (compatible con OpenAI).

type chatRequest struct {
	Model          string         `json:"model"`
	Messages       []message      `json:"messages"`
	ResponseFormat responseFormat `json:"response_format"`
	Provider       providerPrefs  `json:"provider"`
	Reasoning      *reasoning     `json:"reasoning,omitempty"` // nil = lo que decida el modelo
}

// reasoning controla cuánto "piensa" el modelo antes de responder.
type reasoning struct {
	Effort  string `json:"effort"`  // none, minimal, low, medium, high
	Exclude bool   `json:"exclude"` // no devolver el texto del razonamiento (no lo usamos)
}

type message struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string o []contentPart
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type responseFormat struct {
	Type       string     `json:"type"`
	JSONSchema jsonSchema `json:"json_schema"`
}

type jsonSchema struct {
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

// providerPrefs limita a qué proveedores puede enviar OpenRouter la factura.
type providerPrefs struct {
	DataCollection    string `json:"data_collection"`    // "deny": sin proveedores que guardan o entrenan con datos
	ZDR               bool   `json:"zdr"`                // solo endpoints con retención cero
	RequireParameters bool   `json:"require_parameters"` // solo proveedores que soportan el esquema JSON
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		Cost                    float64 `json:"cost"`
		PromptTokens            int     `json:"prompt_tokens"`
		CompletionTokens        int     `json:"completion_tokens"`
		CompletionTokensDetails struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage"`
}
