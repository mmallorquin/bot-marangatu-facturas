// Package invoice define una factura paraguaya y valida que sus datos sean coherentes.
package invoice

// Tipos de comprobante que el bot reconoce.
const (
	TypeFactura     = "factura"
	TypeNotaCredito = "nota_credito"
	TypeNotaDebito  = "nota_debito"
	TypeAutofactura = "autofactura"
	TypeTicket      = "ticket"
	TypeOther       = "otro"
)

// Condiciones de venta.
const (
	ConditionCash   = "contado"
	ConditionCredit = "credito"
)

// CurrencyPYG es el guaraní, la única moneda soportada por ahora.
const CurrencyPYG = "PYG"

// Invoice son los datos de un comprobante. Los montos están en guaraníes
// e incluyen IVA, como se imprimen en las facturas paraguayas.
// Las etiquetas JSON coinciden con el esquema que se le pide al modelo de IA.
type Invoice struct {
	IsInvoice       bool     `json:"es_comprobante"`
	Type            string   `json:"tipo"`
	IssuerRUC       string   `json:"ruc_emisor"`
	IssuerName      string   `json:"razon_social_emisor"`
	Timbrado        string   `json:"timbrado"`
	Number          string   `json:"numero"`
	Date            string   `json:"fecha"` // AAAA-MM-DD
	Condition       string   `json:"condicion"`
	Currency        string   `json:"moneda"`
	Exempt          int64    `json:"exentas"`
	Taxed5          int64    `json:"gravada_5"`
	Taxed10         int64    `json:"gravada_10"`
	VAT5            int64    `json:"iva_5"`
	VAT10           int64    `json:"iva_10"`
	Total           int64    `json:"total"`
	CDC             string   `json:"cdc"` // solo facturas electrónicas (SIFEN)
	UncertainFields []string `json:"campos_dudosos"`
}
