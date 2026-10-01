package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"mawg/internal/magitrickle"
	"mawg/internal/platform"
	"mawg/internal/platform/keenetic"
	"mawg/internal/platform/openwrt"
	"mawg/internal/rotator"
	"mawg/internal/store"
	"mawg/internal/web"
)

const version = "0.1.0"

func detectPlatform() (string, string) {
	if _, err := os.Stat("/etc/openwrt_release"); err == nil {
		return store.PlatformOpenwrt, "/etc/mawg"
	}
	if _, err := os.Stat("/bin/ndmc"); err == nil {
		return store.PlatformKeenetic, "/opt/etc/mawg"
	}
	if _, err := os.Stat("/opt/bin/ndmc"); err == nil {
		return store.PlatformKeenetic, "/opt/etc/mawg"
	}
	return "", ""
}

const logMaxBytes = 256 * 1024

type rotatingWriter struct {
	path string
	file *os.File
	size int64
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	if w.size+int64(len(p)) > logMaxBytes {
		w.file.Close()
		f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return 0, err
		}
		w.file = f
		w.size = 0
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func setupLogging(platform string) {
	var path string
	switch platform {
	case store.PlatformKeenetic:
		path = "/tmp/mawg.log"
	default:
		path = "/var/log/mawg.log"
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	info, _ := f.Stat()
	rw := &rotatingWriter{path: path, file: f}
	if info != nil {
		rw.size = info.Size()
	}
	log.SetOutput(io.MultiWriter(os.Stderr, rw))
}

func main() {
	platformName := flag.String("platform", "", "platform override: openwrt | keenetic")
	base := flag.String("base", "", "config directory")
	port := flag.Int("port", 8090, "web ui port")
	flag.Parse()

	plat := *platformName
	dir := *base
	if plat == "" || dir == "" {
		detected, defaultDir := detectPlatform()
		if plat == "" {
			plat = detected
		}
		if dir == "" {
			dir = defaultDir
		}
	}
	if plat == "" || dir == "" {
		log.Fatal("cannot detect platform, use -platform and -base")
	}
	setupLogging(plat)

	var backend platform.Backend
	switch plat {
	case store.PlatformOpenwrt:
		backend = openwrt.New()
	case store.PlatformKeenetic:
		backend = keenetic.New()
	default:
		log.Fatalf("unknown platform %q", plat)
	}
	if err := backend.Detect(); err != nil {
		log.Fatalf("platform detect failed: %v", err)
	}

	st, err := store.Open(dir)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	mt := magitrickle.New("http://127.0.0.1:8080")
	engine := rotator.New(st, backend, mt)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	engine.Start(ctx)

	srv := web.New(st, engine, backend, mt, version)
	addr := fmt.Sprintf(":%d", *port)
	log.Printf("mawg %s: platform=%s base=%s web=http://0.0.0.0:%d", version, plat, dir, *port)
	httpServer := &http.Server{Addr: addr, Handler: srv.Handler()}
	go func() {
		<-ctx.Done()
		httpServer.Close()
	}()
	if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
	engine.Stop()
}
