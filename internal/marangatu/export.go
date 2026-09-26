// Package marangatu arma el archivo para importar compras en el Registro de Comprobantes
// de Marangatu (RG 90), según la "Especificación Técnica para Importación" de la DNIT (junio 2021).
// Ver docs/rg90-compras.md.
package marangatu

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
)

const (
	// Límite de filas por archivo que acepta Marangatu.
	maxRows = 5000
	// Cantidad de campos de un registro de compras.
	fieldCount = 20
	// Largo máximo de la razón social.
	maxNameLength = 250

	recordTypePurchase = "2"  // Tabla 1: COMPRAS
	identificationRUC  = "11" // Tabla 3: RUC
	conditionCash      = "1"  // Tabla 2: CONTADO
	conditionCredit    = "2"  // Tabla 2: CRÉDITO
	yes, no            = "S", "N"

	fieldSeparator = "\t" // formato .TXT delimitado por tabulaciones
	lineEnding     = "\r\n"
	dateLayout     = "02/01/2006"
	fileExtension  = ".txt"
)

// Razones por las que una factura no entra en el archivo.
const (
	ReasonElectronic      = "es electrónica: Marangatu la trae sola"
	ReasonUnsupportedType = "tipo de comprobante todavía no soportado"
	ReasonTooOld          = "fecha anterior al 01/01/2021 al contado"
)

// Tabla 4: códigos de tipo de comprobante soportados.
var receiptTypeCodes = map[string]string{
	invoice.TypeFactura: "109",
	invoice.TypeTicket:  "112",
}

// Tipos que solo informan el monto total (campos 9 a 11 en 0).
var totalOnlyTypes = map[string]bool{invoice.TypeTicket: true}

// Fechas anteriores no se aceptan, salvo compras a crédito.
var oldestAllowedDate = time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)

var (
	fileIDPattern = regexp.MustCompile(`^[A-Za-z0-9]{1,5}$`)
	lineBreaks    = strings.NewReplacer("\t", " ", "\r", " ", "\n", " ")
)

var (
	ErrInvalidSettings = errors.New("configuración incompleta")
	ErrInvalidPeriod   = errors.New("período inválido")
	ErrInvalidFileID   = errors.New("identificador de archivo inválido")
	ErrNothingToExport = errors.New("no hay facturas para exportar")
	ErrTooManyRows     = errors.New("demasiadas facturas para un solo archivo")
)

// Settings son los datos del contribuyente que informa.
type Settings struct {
	RUC       string // RUC del contribuyente, con dígito verificador
	ImputeIVA bool
	ImputeIRE bool
	ImputeIRP bool // IRP-RSP
}

// Validate verifica que se pueda armar el archivo.
func (s Settings) Validate() error {
	if err := invoice.ValidateRUC(s.RUC); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSettings, err)
	}
	if !s.ImputeIVA && !s.ImputeIRE && !s.ImputeIRP {
		return fmt.Errorf("%w: elegí al menos un impuesto al que imputar (IVA, IRE o IRP-RSP)", ErrInvalidSettings)
	}
	return nil
}

// Skipped es una factura que quedó fuera del archivo.
type Skipped struct {
	Number string
	Reason string
}

// Export es el archivo listo para subir a Marangatu.
type Export struct {
	FileName string // <RUC>_REG_MMAAAA_<ID>.zip
	Zip      []byte
	Rows     int
	Skipped  []Skipped
}

// FileID genera el identificador del archivo (hasta 5 caracteres) a partir de un número de secuencia.
func FileID(seq int) string {
	if seq <= 9999 {
		return fmt.Sprintf("V%04d", seq)
	}
	return fmt.Sprintf("%05d", seq)
}

// BuildPurchases arma el ZIP de compras de un período (AAAA-MM).
// Las facturas que no se pueden importar se informan en Export.Skipped.
func BuildPurchases(invoices []invoice.Invoice, s Settings, period, fileID string) (Export, error) {
	if err := s.Validate(); err != nil {
		return Export{}, err
	}
	month, err := time.Parse("2006-01", period)
	if err != nil {
		return Export{}, fmt.Errorf("%w: %q", ErrInvalidPeriod, period)
	}
	if !fileIDPattern.MatchString(fileID) {
		return Export{}, fmt.Errorf("%w: %q", ErrInvalidFileID, fileID)
	}

	var (
		lines   strings.Builder
		rows    int
		skipped []Skipped
	)
	for _, inv := range invoices {
		if reason := skipReason(inv); reason != "" {
			skipped = append(skipped, Skipped{Number: inv.Number, Reason: reason})
			continue
		}
		lines.WriteString(purchaseRow(inv, s) + lineEnding)
		rows++
	}
	if rows == 0 {
		return Export{Skipped: skipped}, ErrNothingToExport
	}
	if rows > maxRows {
		return Export{}, fmt.Errorf("%w: %d (máximo %d)", ErrTooManyRows, rows, maxRows)
	}

	baseName := fmt.Sprintf("%s_REG_%s_%s", rucBase(s.RUC), month.Format("012006"), fileID)
	data, err := zipSingleFile(baseName+fileExtension, []byte(lines.String()))
	if err != nil {
		return Export{}, err
	}
	return Export{FileName: baseName + ".zip", Zip: data, Rows: rows, Skipped: skipped}, nil
}

func skipReason(inv invoice.Invoice) string {
	if inv.CDC != "" {
		return ReasonElectronic
	}
	if _, ok := receiptTypeCodes[inv.Type]; !ok {
		return ReasonUnsupportedType
	}
	date, err := time.Parse("2006-01-02", inv.Date)
	if err == nil && date.Before(oldestAllowedDate) && inv.Condition != invoice.ConditionCredit {
		return ReasonTooOld
	}
	return ""
}

// purchaseRow arma los 20 campos de un registro de compras, en el orden de la especificación.
func purchaseRow(inv invoice.Invoice, s Settings) string {
	taxed10, taxed5, exempt := inv.Taxed10, inv.Taxed5, inv.Exempt
	if totalOnlyTypes[inv.Type] {
		taxed10, taxed5, exempt = 0, 0, 0
	}

	fields := []string{
		recordTypePurchase,           // 1  código tipo de registro
		identificationRUC,            // 2  tipo de identificación del proveedor
		rucBase(inv.IssuerRUC),       // 3  RUC del proveedor sin DV
		sanitizeName(inv.IssuerName), // 4  razón social
		receiptTypeCodes[inv.Type],   // 5  tipo de comprobante
		displayDate(inv.Date),        // 6  fecha de emisión dd/mm/aaaa
		inv.Timbrado,                 // 7  timbrado
		inv.Number,                   // 8  número ###-###-#######
		amount(taxed10),              // 9  gravado 10 % (IVA incluido)
		amount(taxed5),               // 10 gravado 5 % (IVA incluido)
		amount(exempt),               // 11 no gravado o exento
		amount(inv.Total),            // 12 total
		conditionCode(inv.Condition), // 13 condición de compra
		no,                           // 14 operación en moneda extranjera
		yesNo(s.ImputeIVA),           // 15 imputa al IVA
		yesNo(s.ImputeIRE),           // 16 imputa al IRE
		yesNo(s.ImputeIRP),           // 17 imputa al IRP-RSP
		no,                           // 18 no imputa
		"",                           // 19 número de comprobante asociado (solo notas de crédito/débito)
		"",                           // 20 timbrado del comprobante asociado
	}
	return strings.Join(fields, fieldSeparator)
}

// rucBase quita el dígito verificador: "80024627-6" → "80024627".
func rucBase(ruc string) string {
	base, _, _ := strings.Cut(strings.TrimSpace(ruc), "-")
	return base
}

func sanitizeName(name string) string {
	clean := strings.Join(strings.Fields(lineBreaks.Replace(name)), " ")
	if runes := []rune(clean); len(runes) > maxNameLength {
		clean = string(runes[:maxNameLength])
	}
	return clean
}

func displayDate(date string) string {
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return parsed.Format(dateLayout)
}

func amount(n int64) string {
	return strconv.FormatInt(n, 10)
}

func conditionCode(condition string) string {
	if condition == invoice.ConditionCredit {
		return conditionCredit
	}
	return conditionCash
}

func yesNo(b bool) string {
	if b {
		return yes
	}
	return no
}

// zipSingleFile comprime un único archivo, como pide Marangatu.
func zipSingleFile(name string, content []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create(name)
	if err != nil {
		return nil, fmt.Errorf("creando el ZIP: %w", err)
	}
	if _, err := f.Write(content); err != nil {
		return nil, fmt.Errorf("escribiendo el ZIP: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("cerrando el ZIP: %w", err)
	}
	return buf.Bytes(), nil
}
