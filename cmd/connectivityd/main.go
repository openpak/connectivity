package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openpak/connectivity"
)

func main() {
	path := flag.String("config", "/app/config.json", "private JSON configuration file")
	flag.Parse()
	if err := run(*path); err != nil {
		log.Fatal(err)
	}
}

func load(path string) (connectivity.Config, error) {
	var cfg connectivity.Config
	f, err := os.Open(path)
	if err != nil {
		return cfg, fmt.Errorf("cannot open config")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return cfg, fmt.Errorf("config must be a regular private file (chmod 600)")
	}
	if info.Size() > 65536 {
		return cfg, fmt.Errorf("config too large")
	}
	d := json.NewDecoder(io.LimitReader(f, 65537))
	d.DisallowUnknownFields()
	if err := d.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("invalid config JSON")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return cfg, fmt.Errorf("trailing config data")
	}
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:20150"
	}
	return cfg, nil
}

func run(path string) error {
	cfg, err := load(path)
	if err != nil {
		return err
	}
	s, err := connectivity.New(cfg)
	if err != nil {
		return err
	}
	defer s.Close()
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("cannot bind HTTP listener")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if cfg.StunListen != "" {
		st, err := connectivity.StartSTUN(ctx, cfg)
		if err != nil {
			return err
		}
		defer st.Close()
	}
	httpServer := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	result := make(chan error, 1)
	go func() { result <- httpServer.Serve(ln) }()
	log.Print("connectivity service ready")
	select {
	case err := <-result:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("HTTP server stopped")
		}
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdown); err != nil {
		httpServer.Close()
		return fmt.Errorf("HTTP shutdown timed out")
	}
	return nil
}
