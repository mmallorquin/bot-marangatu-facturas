package telegram

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/marangatu"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

// Estos tests comprueban las instrucciones que recibe el usuario en cada rama,
// no solo una constante que podría no llegar a mostrarse en Telegram.
func TestDNITHelpDistinguishesTheBotFromPresentation(t *testing.T) {
	h := newHarness(t)
	h.sendText("/ayuda")
	welcome := h.telegram.byMethod("sendMessage")[0].text
	for _, want := range []string{"no presenta", "confirmar", "Talón de Presentación", "comprobantes físicos"} {
		if !strings.Contains(welcome, want) {
			t.Errorf("la ayuda debe aclarar %q: %s", want, welcome)
		}
	}
}

func TestDNITZipDeliveryRequiresConfirmationInMarangatu(t *testing.T) {
	for _, period := range []string{"09/2026", "2026"} {
		t.Run(period, func(t *testing.T) {
			h := newHarness(t)
			h.saveOneInvoice(t)
			h.sendText("/ruc 80024627-6")
			h.sendText("/imputar irp")
			registration := "955"
			if period == "2026" {
				registration = "956"
			}
			h.sendText("/registro " + registration)
			h.sendText("/exportar " + period)
			callbackPeriod := "2026-09"
			if period == "2026" {
				callbackPeriod = "2026"
			}
			h.pressExport(t, "x:z:"+callbackPeriod)
			docs := h.telegram.byMethod("sendDocument")
			if len(docs) != 1 {
				t.Fatalf("se esperaba un ZIP, hubo %d archivos", len(docs))
			}
			for _, want := range []string{"Importar el ZIP no confirma", "confirmar", "Talón de Presentación", "comprobantes físicos", "imputación"} {
				if !strings.Contains(docs[0].text, want) {
					t.Errorf("el ZIP debe incluir la instrucción %q: %s", want, docs[0].text)
				}
			}
		})
	}
}

func TestDNITElectronicInvoiceRequestsImputationReview(t *testing.T) {
	h := newHarness(t)
	h.reader.result.Invoice.CDC = strings.Repeat("1", 44)
	h.sendPhoto()
	text := h.telegram.lastSent(t).text
	for _, want := range []string{"no va en el ZIP", "Marangatu", "imputación", "si no se imputó automáticamente"} {
		if !strings.Contains(text, want) {
			t.Errorf("la factura electrónica debe aclarar %q: %s", want, text)
		}
	}
}

func TestDNITExportMessagesFitWithManyElectronicInvoices(t *testing.T) {
	for _, withPaper := range []bool{false, true} {
		t.Run(fmt.Sprintf("con_papel_%t", withPaper), func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			for i := 1; i <= 60; i++ {
				inv := sampleInvoice()
				inv.Number = fmt.Sprintf("001-001-%07d", i)
				if !withPaper || i > exportPreviewLimit {
					inv.CDC = strings.Repeat("1", 44)
				}
				id, err := h.store.CreateDraft(ctx, testChatID, store.Draft{Invoice: inv})
				if err != nil {
					t.Fatal(err)
				}
				if err := h.store.Save(ctx, testChatID, id); err != nil {
					t.Fatal(err)
				}
			}
			h.sendText("/ruc 80024627-6")
			h.sendText("/imputar irp")
			h.sendText("/registro 956")
			h.sendText("/exportar 2026")
			text := h.telegram.lastSent(t).text
			if units := len(utf16.Encode([]rune(text))); units > 4096 {
				t.Fatalf("el mensaje de exportación excede el límite de Telegram: %d", units)
			}
			for _, want := range []string{"Quedaron afuera", "más excluidos", "imputación"} {
				if !strings.Contains(text, want) {
					t.Errorf("el mensaje debe conservar %q: %s", want, text)
				}
			}
			if withPaper {
				h.pressExport(t, "x:z:2026")
				docs := h.telegram.byMethod("sendDocument")
				if len(docs) != 1 {
					t.Fatalf("se esperaba un ZIP, hubo %d", len(docs))
				}
			} else {
				for _, want := range []string{"Talón de Presentación", "comprobantes físicos"} {
					if !strings.Contains(text, want) {
						t.Errorf("el mensaje debe conservar %q", want)
					}
				}
			}
		})
	}
}

func TestDNITExportWithOnlyElectronicInvoicesRequestsImputationReview(t *testing.T) {
	h := newHarness(t)
	h.reader.result.Invoice.CDC = strings.Repeat("1", 44)
	h.saveOneInvoice(t)
	h.sendText("/ruc 80024627-6")
	h.sendText("/imputar irp")
	h.sendText("/registro 956")
	h.sendText("/exportar 2026")
	text := h.telegram.lastSent(t).text
	for _, want := range []string{"ninguno entra en el ZIP", "imputación", "confirmar", "Marangatu"} {
		if !strings.Contains(text, want) {
			t.Errorf("sin ZIP por electrónicas debe explicar %q: %s", want, text)
		}
	}
	if docs := h.telegram.byMethod("sendDocument"); len(docs) != 0 {
		t.Fatal("las electrónicas no deben exportarse en un ZIP")
	}
}

func TestDNITEmptyBotDoesNotDeclareNoMovement(t *testing.T) {
	h := newHarness(t)
	h.sendText("/ruc 80024627-6")
	h.sendText("/imputar irp")
	h.sendText("/registro 956")
	h.sendText("/exportar 2026")
	text := h.telegram.lastSent(t).text
	for _, want := range []string{"No hay facturas guardadas", "no significa que no hubo operaciones", "Si realmente", "sin movimiento", "Marangatu"} {
		if !strings.Contains(text, want) {
			t.Errorf("sin facturas debe explicar la condición %q: %s", want, text)
		}
	}
	if docs := h.telegram.byMethod("sendDocument"); len(docs) != 0 {
		t.Fatal("el bot vacío no debe fabricar un ZIP sin movimiento")
	}
}

func TestDNITZipCaptionKeepsGuidanceWithinTelegramLimit(t *testing.T) {
	export := marangatu.Export{Rows: 1}
	for i := 1; i <= 20; i++ {
		export.Skipped = append(export.Skipped, marangatu.Skipped{
			Number: fmt.Sprintf("001-001-%07d", i), Reason: marangatu.ReasonElectronic,
		})
	}
	for _, period := range []string{"2026-09", "2026"} {
		t.Run(period, func(t *testing.T) {
			caption := exportCaption(period, export, true)
			if units := len(utf16.Encode([]rune(caption))); units > 1024 {
				t.Fatalf("el texto del ZIP excede el límite de Telegram: %d", units)
			}
			for _, want := range []string{"Talón de Presentación", "comprobantes físicos", "más; revisá la previa"} {
				if !strings.Contains(caption, want) {
					t.Errorf("el texto debe conservar %q: %s", want, caption)
				}
			}
		})
	}
}
