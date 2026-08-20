// Command leasereg runs the lease-registry HTTP server. A single SQLite file
// is the source of truth; on startup the server reopens that file, runs a
// recovery sweep to drop leases that elapsed while the process was stopped,
// and serves the HTTP API. The fencing-token counter sequence is preserved
// across restarts because it lives in the same database.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"leasereg/internal/httpapi"
	"leasereg/internal/lease"
	"leasereg/internal/store"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	dbPath := flag.String("db", "leases.db", "SQLite database path")
	smoke := flag.Bool("smoke-test", false, "run an in-process smoke test and exit")
	flag.Parse()

	if *smoke {
		os.Exit(runSmoke())
	}

	srv, err := start(*dbPath, *addr)
	if err != nil {
		log.Fatalf("start: %v", err)
	}
	defer srv.store.Close()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	log.Printf("leasereg listening on %s (db=%s)", *addr, *dbPath)
	<-stop
	log.Printf("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.http.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("shutdown: %v", err)
	}
}

// assembledServer holds the pieces needed to run and stop the service.
type assembledServer struct {
	store *store.Store
	http  *http.Server
}

func start(dbPath, addr string) (*assembledServer, error) {
	st, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	mgr := lease.NewManager(st, lease.RealClock{})
	if n, err := mgr.RestartRecover(context.Background()); err != nil {
		st.Close()
		return nil, err
	} else if n > 0 {
		log.Printf("startup recovery: removed %d expired leases", n)
	}
	srv := httpapi.New(mgr)
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           httpapi.NewRouter(srv),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()
	return &assembledServer{store: st, http: httpSrv}, nil
}
