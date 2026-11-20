package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"time"
)

//go:embed public/*
var content embed.FS

func main() {
	sub, err := fs.Sub(content, "public")
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{
		Addr:              ":8080",
		Handler:           http.FileServer(http.FS(sub)),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Println("openksm-web listening on :8080")
	log.Fatal(srv.ListenAndServe())
}
