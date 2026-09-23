// Command ginapp registers routes through gin: groups with middleware, Use,
// Handle, Any, Static and a helper taking a *gin.RouterGroup.
package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func main() {
	r := gin.New()
	r.Use(logging)
	r.GET("/health", health)
	r.Static("/assets", "./assets")
	v1 := r.Group("/v1")
	v1.Use(authRequired())
	{
		v1.GET("/notes", listNotes)
		v1.POST("/notes", audit, createNote)
		admin := v1.Group("/admin", adminOnly)
		admin.DELETE("/notes/:id", deleteNote)
	}
	registerUsers(r.Group("/users"))
	r.Any("/ping", ping)
	r.Handle(http.MethodPatch, "/settings", updateSettings)
	_ = r.Run()
}

func registerUsers(g *gin.RouterGroup) {
	g.GET("/:id", getUser)
	g.PUT("/:id", authRequired(), updateUser)
}

func health(c *gin.Context)         {}
func listNotes(c *gin.Context)      {}
func createNote(c *gin.Context)     {}
func deleteNote(c *gin.Context)     {}
func getUser(c *gin.Context)        {}
func updateUser(c *gin.Context)     {}
func ping(c *gin.Context)           {}
func updateSettings(c *gin.Context) {}

// authenticate checks the request's bearer token.
func authenticate(c *gin.Context) bool { return c.GetHeader("Authorization") != "" }

func logging(c *gin.Context)   { c.Next() }
func audit(c *gin.Context)     { c.Next() }
func adminOnly(c *gin.Context) { c.Next() }

func authRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !authenticate(c) {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	}
}
