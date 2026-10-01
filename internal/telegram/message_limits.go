package telegram

import "unicode/utf16"

// Telegram rejects an oversized message together with its review buttons. Only
// the displayed text is shortened; invoice data and keyboards stay intact.
func telegramMessageText(text string) string {
	const limit = 4096
	const suffix = "\n\n… Mensaje abreviado para mostrarlo en Telegram."
	units := 0
	for _, r := range text {
		units += utf16.RuneLen(r)
	}
	if units <= limit {
		return text
	}
	remaining := limit - len(utf16.Encode([]rune(suffix)))
	for index, r := range text {
		remaining -= utf16.RuneLen(r)
		if remaining < 0 {
			return text[:index] + suffix
		}
	}
	return text
}
