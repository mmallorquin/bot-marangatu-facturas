package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/store"
)

func TestGuidedSetupAsksRUCThenImputationsWithButtons(t *testing.T) {
	// Arrange
	h := newHarness(t)

	// Act: /start, un "hola", un RUC mal escrito, el RUC bien y los botones de impuestos.
	h.sendText("/start")
	askRUC := h.telegram.lastSent(t).text
	h.sendText("hola")
	hello := h.telegram.lastSent(t).text
	h.sendText("1234567-1")
	wrong := h.telegram.lastSent(t).text
	h.sendText("80024627-6")
	askTaxes := h.telegram.lastSent(t)
	h.pressRaw("i:t:4") // marcar IRP-RSP
	marked := h.telegram.byMethod("editMessageReplyMarkup")
	h.pressRaw("i:ok:4")
	edits := h.telegram.byMethod("editMessageText")
	done := edits[len(edits)-1].text

	// Assert
	if !strings.Contains(askRUC, "necesito tu RUC") {
		t.Errorf("/start debería pedir el RUC: %q", askRUC)
	}
	if hello != HelpMessage {
		t.Errorf("un saludo recibe la ayuda, no un error de RUC: %q", hello)
	}
	if !strings.Contains(wrong, "dígito verificador") || !strings.Contains(wrong, "/cancelar") {
		t.Errorf("RUC inválido: %q", wrong)
	}
	if !strings.Contains(askTaxes.text, "¿A qué impuestos") || !strings.Contains(askTaxes.markup, `"i:t:1"`) {
		t.Errorf("después del RUC pide los impuestos con botones: %+v", askTaxes)
	}
	if len(marked) == 0 || !strings.Contains(marked[len(marked)-1].markup, "☑️ IRP-RSP") {
		t.Errorf("el botón tocado queda marcado: %+v", marked)
	}
	if !strings.Contains(done, "IRP-RSP") || !strings.Contains(done, "/exportar 2026") || !strings.Contains(done, "Todo listo") {
		t.Errorf("confirmación: %q", done)
	}
	cs, _ := h.store.Settings(context.Background(), testChatID)
	if cs.RUC != "80024627-6" || cs.Imputations != (store.Imputations{IRP: true}) || cs.AwaitingRUC {
		t.Errorf("configuración = %+v", cs)
	}
}

func TestStartWithRUCAlreadyConfiguredDoesNotAskAgain(t *testing.T) {
	h := newHarness(t)
	h.sendText("/ruc 80024627-6")

	h.sendText("/start")

	if got := h.telegram.lastSent(t).text; got != WelcomeMessage {
		t.Errorf("con el RUC cargado solo muestra la bienvenida: %q", got)
	}
}

func TestImputeWithoutArgumentsShowsButtonsAndDoneNeedsOne(t *testing.T) {
	h := newHarness(t)

	h.sendText("/imputar")
	prompt := h.telegram.lastSent(t)
	h.pressRaw("i:ok:0")
	answers := h.telegram.byMethod("answerCallbackQuery")

	if !strings.Contains(prompt.markup, "✅ Listo") {
		t.Errorf("/imputar sin nada muestra los botones: %+v", prompt)
	}
	if last := answers[len(answers)-1]; !last.alert || !strings.Contains(last.text, "al menos un impuesto") {
		t.Errorf("Listo sin elegir nada: %+v", last)
	}
}

func TestCancelLeavesTheGuidedSetup(t *testing.T) {
	h := newHarness(t)
	h.sendText("/start")

	h.sendText("/cancelar")
	h.sendText("123")

	if got := h.telegram.lastSent(t).text; got != HelpMessage {
		t.Errorf("fuera de la configuración, un número no se toma como RUC: %q", got)
	}
}
