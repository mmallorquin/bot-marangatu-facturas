// Package reader define el contrato para leer facturas desde una imagen,
// independiente del proveedor de IA que se use.
package reader

import (
	"context"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
)

// Image es una foto o un PDF de factura, listo para enviar al modelo.
type Image struct {
	Data     []byte
	MimeType string // image/jpeg, image/png, image/webp o application/pdf
}

// Result es lo que devolvió el modelo, más datos para medir costo.
type Result struct {
	Invoice invoice.Invoice
	Model   string  // modelo que respondió
	CostUSD float64 // costo informado por el proveedor
	Usage   Usage
}

// Usage son los tokens que consumió la lectura.
// Los de razonamiento se cobran como salida y ya están incluidos en OutputTokens.
type Usage struct {
	InputTokens     int
	OutputTokens    int
	ReasoningTokens int
}

// Reader lee los datos de una factura desde una imagen.
type Reader interface {
	Read(ctx context.Context, img Image) (Result, error)
}

// ErrorKind distingue la próxima acción útil sin revelar detalles del proveedor.
type ErrorKind string

const (
	ErrorTransient     ErrorKind = "transient"
	ErrorQuota         ErrorKind = "quota"
	ErrorConfiguration ErrorKind = "configuration"
	ErrorDocument      ErrorKind = "document"
	ErrorResponse      ErrorKind = "response"
)

// ClassifiedError permite explicar una falla al usuario usando solo texto seguro.
// Retryable indica que puede volver a enviar el archivo más tarde; no autoriza reintentos automáticos.
type ClassifiedError interface {
	error
	Kind() ErrorKind
	Retryable() bool
	UserMessage() string
}
