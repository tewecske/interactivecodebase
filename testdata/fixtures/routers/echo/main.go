// Command echoapp registers routes through labstack/echo: groups with
// middleware, Use, route middleware, Add, Any and Static.
package main

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

func main() {
	e := echo.New()
	e.Use(logging)
	e.GET("/", home)
	e.Static("/static", "assets")
	api := e.Group("/api", requireAuth)
	api.GET("/notes", listNotes)
	api.POST("/notes", createNote, audit)
	admin := api.Group("/admin")
	admin.Use(adminOnly)
	admin.DELETE("/notes/:id", deleteNote)
	e.Add(http.MethodPut, "/profile", updateProfile)
	e.Any("/ping", ping)
	e.Logger.Fatal(e.Start(":8080"))
}

func home(c echo.Context) error          { return nil }
func listNotes(c echo.Context) error     { return nil }
func createNote(c echo.Context) error    { return nil }
func deleteNote(c echo.Context) error    { return nil }
func updateProfile(c echo.Context) error { return nil }
func ping(c echo.Context) error          { return nil }

// authenticate checks the request's bearer token.
func authenticate(c echo.Context) bool { return c.Request().Header.Get("Authorization") != "" }

func logging(next echo.HandlerFunc) echo.HandlerFunc   { return next }
func audit(next echo.HandlerFunc) echo.HandlerFunc     { return next }
func adminOnly(next echo.HandlerFunc) echo.HandlerFunc { return next }

func requireAuth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if !authenticate(c) {
			return c.NoContent(http.StatusUnauthorized)
		}
		return next(c)
	}
}
