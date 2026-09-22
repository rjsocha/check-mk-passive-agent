package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	log.SetFlags(0)

	settings, err := loadSettings()
	if err != nil {
		log.Fatal(err)
	}
	config, err := loadConfig(settings.ConfigFile)
	if err != nil {
		log.Fatal(err)
	}
	if st, err := os.Stat(settings.Storage); err != nil || !st.IsDir() {
		log.Fatalf("storage %s: not a directory", settings.Storage)
	}

	server := &http.Server{
		Addr: settings.Listen,
		Handler: &Receiver{
			Config:     config,
			Storage:    settings.Storage,
			MaxBody:    settings.MaxBody,
			MaxPayload: settings.MaxPayload,
		},
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()

	log.Printf("check-mk-passive-agent %s listening on %s, storage %s, tokens %d",
		version, settings.Listen, settings.Storage, len(config.Tokens))
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
