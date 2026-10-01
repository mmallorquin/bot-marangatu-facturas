package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// Catches a primary menu that exposes configuration commands as separate flows.
func TestConfigureMenuPublishesFourPrimaryFlows(t *testing.T) {
	var commands []models.BotCommand
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		if r.URL.Path == "/bot"+testToken+"/setMyCommands" {
			_ = json.Unmarshal([]byte(r.FormValue("commands")), &commands)
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
	}))
	t.Cleanup(server.Close)
	b, err := bot.New(testToken, bot.WithServerURL(server.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatal(err)
	}
	if err := ConfigureMenu(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, command := range commands {
		got = append(got, command.Command)
	}
	if !reflect.DeepEqual(got, []string{"facturas", "exportar", "ajustes", "ayuda"}) {
		t.Fatalf("menú = %v", got)
	}
}
