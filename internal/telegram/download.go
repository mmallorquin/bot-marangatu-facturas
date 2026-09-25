package telegram

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-telegram/bot"
)

const downloadTimeout = 30 * time.Second

var downloadClient = &http.Client{Timeout: downloadTimeout}

// downloadFile baja un archivo de Telegram. Ojo: la URL de descarga incluye el token,
// así que los errores de acá se tienen que enmascarar antes de loguearlos.
func downloadFile(ctx context.Context, b *bot.Bot, fileID string) ([]byte, error) {
	file, err := b.GetFile(ctx, &bot.GetFileParams{FileID: fileID})
	if err != nil {
		return nil, fmt.Errorf("pidiendo el archivo a Telegram: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.FileDownloadLink(file), nil)
	if err != nil {
		return nil, fmt.Errorf("armando la descarga: %w", err)
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("descargando el archivo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Telegram respondió %d al descargar", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("leyendo el archivo: %w", err)
	}
	if len(data) > maxImageBytes {
		return nil, fmt.Errorf("el archivo supera %d bytes", maxImageBytes)
	}
	return data, nil
}
