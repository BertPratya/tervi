// Command server runs the tervi central server.
package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	addr := os.Getenv("SERVER_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	log.Printf("listening on %s", addr)
	if err := http.ListenAndServe(addr, newMux()); err != nil {
		log.Fatal(err)
	}
}

// newMux returns the server's routes.
func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth)
	return mux
}

// handleHealth reports that the server is running.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
}
