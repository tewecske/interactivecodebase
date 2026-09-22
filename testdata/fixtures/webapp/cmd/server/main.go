// Command server wires the webapp fixture together.
package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"

	"example.com/webapp/internal/config"
	"example.com/webapp/internal/mail"
	"example.com/webapp/internal/notes"
	"example.com/webapp/internal/store/postgres"
	"example.com/webapp/internal/weather"
	"example.com/webapp/internal/web"
)

func main() {
	cfg := config.Load(os.Getenv)
	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	sessions := postgres.NewSessionRepository(db)
	service := notes.NewService(postgres.NewNoteRepository(db), mail.NewSMTPMailer(cfg.SMTPAddr, cfg.MailFrom))
	handler := web.New(web.Deps{
		Auth:      web.NewAuthenticator(sessions),
		Notes:     service,
		Weather:   weather.NewClient(cfg.WeatherAPIKey),
		Providers: cfg.OAuthProviders,
		ExportDir: cfg.ExportDir,
	})
	log.Fatal(http.ListenAndServe(cfg.Addr, handler))
}
