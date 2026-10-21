package telegram

import (
	"context"
	"fmt"

	"github.com/go-telegram/bot"

	"github.com/mmallorquin/bot-marangatu-facturas/internal/invoice"
)

// Una tanda son varios archivos del mismo chat leyéndose a la vez (un álbum o fotos seguidas).
// Para que la carga sea ágil, las facturas que se guardan solas se confirman en una línea y las
// que tienen dudas no interrumpen: se muestran de a una cuando termina el último archivo.
// shortcut: la tanda vive en memoria; si el bot se reinicia a mitad, las dudosas quedan en /pendientes.
type batchProgress struct {
	open   int     // archivos de la tanda que todavía no terminaron
	files  int     // archivos que ya terminaron
	saved  int     // guardadas solas, confirmadas en una línea
	review []int64 // borradores con dudas, para mostrar al cerrar la tanda
}

// currentBatch devuelve la tanda del chat si este archivo forma parte de una; nil si llegó solo.
// Se llama con el bloqueo del chat tomado.
func currentBatch(ctx context.Context) *batchProgress {
	c := currentOperation(ctx).chat
	if c.batch.open > 1 || c.batch.files > 0 {
		return &c.batch
	}
	return nil
}

// closeBatch descuenta el archivo terminado. Si era el último de una tanda, resume lo hecho
// y muestra la primera factura que quedó para revisar.
func (h *handler) closeBatch(ctx context.Context, b *bot.Bot, chatID int64) {
	if ctx.Err() != nil {
		return // borrado o apagado: invalidateReads ya reinició la tanda, o el proceso termina
	}
	h.finishRead(ctx, func(ctx context.Context) {
		c := currentOperation(ctx).chat
		c.batch.open--
		c.batch.files++
		if c.batch.open > 0 {
			return
		}
		done := c.batch
		c.batch = batchProgress{}
		if done.files < 2 {
			return // un archivo solo ya se respondió completo
		}
		h.send(ctx, b, chatID, batchSummary(done), nil)
		if len(done.review) > 0 {
			h.offerPending(ctx, b, chatID, done.review[0])
		}
	})
}

func batchSummary(p batchProgress) string {
	var parts []string
	if p.saved > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", p.saved, plural(p.saved, "guardada", "guardadas")))
	}
	if n := len(p.review); n > 0 {
		parts = append(parts, fmt.Sprintf("%d para revisar", n))
	}
	text := fmt.Sprintf("🏁 Terminé los %d archivos", p.files)
	if len(parts) == 0 {
		return text + "."
	}
	text += ": " + joinWithY(parts) + "."
	if n := len(p.review); n > 0 {
		text += " " + plural(n, "Te la muestro abajo 👇", "Te las muestro de a una 👇")
	}
	return text
}

// FormatSavedLine confirma en una línea una factura de la tanda que se guardó sola.
func FormatSavedLine(inv invoice.Invoice) string {
	return fmt.Sprintf("✅ Guardada: %s · %s · %s Gs",
		truncateUTF16(inv.Number, maxNumberDisplayUTF16), truncateUTF16(inv.IssuerName, maxIssuerDisplayUTF16), formatGs(inv.Total))
}
