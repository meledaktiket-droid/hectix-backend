package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"hectix/backend/internal/config"
	"hectix/backend/internal/dbx"
	"hectix/backend/internal/jobs"
	"hectix/backend/internal/payments"
)

func healthz(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) }

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		if err := dbx.Migrate(cfg.DatabaseURL); err != nil {
			log.Fatal(err)
		}
		log.Print("migrations applied")
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := dbx.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}

	waitWorker := (&jobs.Worker{DB: pool, Handlers: jobHandlers(cfg, pool)}).Run(ctx, 4)
	go payments.RunSweeper(ctx, pool)

	srv := &http.Server{Addr: cfg.Addr, Handler: newServer(cfg, pool)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()
	log.Printf("listening on %s", cfg.Addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
	stopped := time.Now()
	<-done

	workerDone := make(chan struct{})
	go func() { waitWorker(); close(workerDone) }()
	select {
	case <-workerDone:
		pool.Close()
	case <-time.After(time.Until(stopped.Add(shutdownBudget))):
		log.Print("shutdown: timed out waiting for in-flight jobs; they will be recovered as stale")
	}
}

const shutdownBudget = 2*time.Minute + 30*time.Second
