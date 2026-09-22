// Package config loads settings from the environment.
package config

import "strings"

// Config holds process settings.
type Config struct {
	Addr           string
	DatabaseURL    string
	SMTPAddr       string
	MailFrom       string
	WeatherAPIKey  string
	ExportDir      string
	OAuthProviders []string
}

// Load reads settings through getenv, typically os.Getenv.
func Load(getenv func(string) string) Config {
	cfg := Config{
		Addr:          getenv("WEBAPP_ADDR"),
		DatabaseURL:   getenv("WEBAPP_DATABASE_URL"),
		SMTPAddr:      getenv("WEBAPP_SMTP_ADDR"),
		MailFrom:      getenv("WEBAPP_MAIL_FROM"),
		WeatherAPIKey: getenv("WEBAPP_WEATHER_API_KEY"),
		ExportDir:     getenv("WEBAPP_EXPORT_DIR"),
	}
	if p := getenv("WEBAPP_OAUTH_PROVIDERS"); p != "" {
		cfg.OAuthProviders = strings.Split(p, ",")
	}
	return cfg
}
