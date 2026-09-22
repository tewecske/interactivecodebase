// Package lang lists supported languages.
package lang

// Code is a language code used as the first path segment.
type Code string

// Supported languages.
const (
	English Code = "en"
	German  Code = "de"
)

// Codes returns all supported languages.
func Codes() []Code {
	return []Code{English, German}
}
