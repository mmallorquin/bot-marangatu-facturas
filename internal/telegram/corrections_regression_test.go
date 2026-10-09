package telegram

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/openrouter"
	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

func setRegressionStore(t *testing.T, h *harness, path string) {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h.store = st
	h.handle = NewHandler(Deps{Logger: discardLogger(), Token: testToken, Reader: h.reader, Store: st,
		Now: func() time.Time { return time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC) }})
}

func addLegacyInvoiceToRegressionDB(t *testing.T, path string, inv invoice.Invoice) int64 {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	data, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("%s|%s|%s|%s", inv.Type, inv.IssuerRUC, inv.Timbrado, inv.Number)
	res, err := db.Exec(`INSERT INTO invoices (chat_id,status,invoice_json,original_json,model,cost_usd,dedup_key,period,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, testChatID, store.StatusSaved, string(data), string(data), "synthetic", 0, key, "2026-09", "2026-09-20T12:00:00Z", "2026-09-20T12:00:05Z")
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestLegacyDuplicateExportCanBeResolvedFromTelegramHistory(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(t.TempDir(), "legacy.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	inv := sampleInvoice()
	addLegacyInvoiceToRegressionDB(t, path, inv)
	inv.IssuerRUC = " " + inv.IssuerRUC + " "
	duplicateID := addLegacyInvoiceToRegressionDB(t, path, inv)
	setRegressionStore(t, h, path)
	h.sendText("/ruc 80024627-6")
	h.sendText("/imputar iva")
	h.sendText("/registro 955")
	h.sendText("/exportar 09/2026")
	if got := h.telegram.lastSent(t); !strings.Contains(got.text, "duplicadas") || !strings.Contains(got.text, "/facturas") || got.markup != "" {
		t.Fatalf("exportación no explica cómo resolver: %+v", got)
	}
	h.sendText("/resumen 09/2026")
	if text := h.telegram.lastSent(t).text; !strings.Contains(text, "totales incluyen duplicados") {
		t.Fatalf("resumen no avisa: %q", text)
	}
	h.sendText("/facturas 09/2026")
	list := h.telegram.lastSent(t)
	if !strings.Contains(list.text, "2 facturas guardadas") || !strings.Contains(list.text, "duplicados") || !strings.Contains(list.markup, "h:o:s:2026-09:0:2") {
		t.Fatalf("historial inaccesible: %+v", list)
	}
	h.pressRaw(historyCallback{action: "o", tab: "s", period: "2026-09", id: duplicateID}.encode())
	edits := h.telegram.byMethod("editMessageText")
	if len(edits) == 0 || !strings.Contains(edits[len(edits)-1].markup, "l:a:2:2026-09") {
		t.Fatal("no se puede abrir/borrar copia")
	}
	h.pressRaw(listCallback{action: listActionAsk, id: duplicateID, period: "2026-09"}.encode())
	h.pressRaw(listCallback{action: listActionConfirm, id: duplicateID, period: "2026-09"}.encode())
	if rec, err := h.store.Get(context.Background(), testChatID, duplicateID); err != nil || rec.Status != store.StatusDeleted || rec.Original.IssuerRUC != inv.IssuerRUC {
		t.Fatal("borrado no preservó original")
	}
	h.sendText("/exportar 09/2026")
	if text := h.telegram.lastSent(t).text; !strings.Contains(text, "1 comprobante") || !strings.Contains(text, "Previa") {
		t.Fatalf("no se recuperó previa: %q", text)
	}
	h.pressExport(t, "x:c:2026-09")
	h.pressExport(t, "x:e:2026-09")
	h.pressExport(t, "x:z:2026-09")
	if docs := h.telegram.byMethod("sendDocument"); len(docs) != 3 || !strings.Contains(docs[2].text, "1 comprobante") {
		t.Fatal("exportación recuperada debe generar CSV/Excel/ZIP de una factura")
	}
}

func TestLegacyDuplicateBlocksAnOldExportConfirmation(t *testing.T) {
	for _, action := range []string{"x:c:", "x:e:", "x:z:"} {
		t.Run(action, func(t *testing.T) {
			h := newHarness(t)
			path := filepath.Join(t.TempDir(), "old_button.db")
			setRegressionStore(t, h, path)
			setupReliableExport(t, h)
			data := callbackDataIn(t, h.telegram.lastSent(t).markup, action)
			confirmation, err := parseExportCallback(data)
			if err != nil {
				t.Fatal(err)
			}
			inv := sampleInvoice()
			inv.IssuerRUC = " " + inv.IssuerRUC + " "
			if err := h.store.Close(); err != nil {
				t.Fatal(err)
			}
			addLegacyInvoiceToRegressionDB(t, path, inv)
			setRegressionStore(t, h, path)
			h.pressRaw(data)
			if docs := h.telegram.byMethod("sendDocument"); len(docs) != 0 {
				t.Fatal("botón anterior exportó una colisión")
			}
			if delivered, err := h.store.ExportRequestStatus(context.Background(), testChatID, "2026-09", confirmation.requestID); err != nil || delivered {
				t.Fatalf("colisión registró una entrega: %v, %v", delivered, err)
			}
			seq, err := h.store.NextExportSeq(context.Background(), testChatID, "2026-09")
			if err != nil || seq != 1 {
				t.Fatalf("colisión consumió secuencia: %d, %v", seq, err)
			}
			answers := h.telegram.byMethod("answerCallbackQuery")
			if len(answers) == 0 || !strings.Contains(answers[len(answers)-1].text, "duplicadas") || !strings.Contains(answers[len(answers)-1].text, "/facturas") {
				t.Fatal("botón anterior no explica recuperación")
			}
		})
	}
}

func TestDuplicateMessageTargetsTheSelectedPeriod(t *testing.T) {
	for _, period := range []string{"2026", "2026-09"} {
		message := duplicateInvoicesMessage(period)
		if !strings.Contains(message, "/facturas "+period) || len(utf16.Encode([]rune(message))) > 200 {
			t.Fatalf("aviso de recuperación incorrecto: %q", message)
		}
	}
}

func TestIncompleteReaderResponseCannotReachAutoSave(t *testing.T) {
	for _, key := range []string{"total", "campos_dudosos"} {
		t.Run(key, func(t *testing.T) {
			inv := sampleInvoice()
			inv.UncertainFields = []string{}
			data, err := json.Marshal(inv)
			if err != nil {
				t.Fatal(err)
			}
			var content map[string]any
			if err := json.Unmarshal(data, &content); err != nil {
				t.Fatal(err)
			}
			delete(content, key)
			data, err = json.Marshal(content)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": string(data)}}}})
			}))
			t.Cleanup(server.Close)
			h := newHarness(t)
			if err := h.store.SetAutoSave(context.Background(), testChatID, true); err != nil {
				t.Fatal(err)
			}
			h.handle = NewHandler(Deps{Logger: discardLogger(), Token: testToken, Store: h.store,
				Reader: openrouter.New(openrouter.Options{BaseURL: server.URL, Model: "synthetic"}),
				Now:    func() time.Time { return time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC) }})
			h.sendPhoto()
			if _, err := h.store.Get(context.Background(), testChatID, 1); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("respuesta incompleta creó factura: %v", err)
			}
			if got := h.telegram.lastSent(t); strings.Contains(got.markup, "g:1") || !strings.Contains(got.text, "respuesta") {
				t.Fatalf("respuesta incompleta no se informa como fallo de lectura: %+v", got)
			}
			m, err := h.store.Metrics(context.Background(), store.MetricsQuery{Since: time.Now().Add(-time.Hour), Now: time.Now()})
			if err != nil || m.ReadErrors != 1 || m.Saved != 0 {
				t.Fatalf("embudo incompleto: %+v, %v", m, err)
			}
		})
	}
}

func TestDecimalCorrectionKeepsOriginalAmountAndPendingInput(t *testing.T) {
	h := newHarness(t)
	id := h.firstDraftID(t)
	h.press(callback{action: actionField, id: id, field: invoice.FieldTotal})
	h.sendText("150.000,00")
	if text := h.telegram.lastSent(t).text; !strings.Contains(text, "no es válido") {
		t.Fatalf("decimal no rechazado: %q", text)
	}
	rec, err := h.store.Get(context.Background(), testChatID, id)
	if err != nil || rec.Invoice.Total != 150000 || rec.Original.Total != 150000 || rec.Corrected {
		t.Fatal("decimal alteró datos originales/actuales")
	}
	if pending, found, err := h.store.Awaiting(context.Background(), testChatID); err != nil || !found || pending.ID != id {
		t.Fatal("decimal perdió corrección pendiente")
	}
	h.sendText("150.000")
	h.press(callback{action: actionSave, id: id})
	if rec, err := h.store.Get(context.Background(), testChatID, id); err != nil || rec.Status != store.StatusSaved || rec.Invoice.Total != 150000 {
		t.Fatal("corrección válida no se recuperó")
	}
}
