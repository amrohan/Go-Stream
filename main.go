package main

import (
	"log"
	"net/http"

	"github.com/gostream/internal/server"
	"github.com/rs/cors"
)

func main() {
	srv, err := server.New()
	if err != nil {
		log.Fatalf("Failed to set up server: %v", err)
	}

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// `AllowCredentials` is crucial for the browser to send/receive cookies.
	c := cors.New(cors.Options{
		AllowedOrigins:   []string{"http://localhost:4200"},
		AllowedMethods:   []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		AllowCredentials: true,
	})

	handler := c.Handler(mux)

	log.Println("🚀 Server running on http://localhost:8080")
	// Use the CORS-wrapped handler instead of the raw mux.
	log.Fatal(http.ListenAndServe(":8080", handler))
}
