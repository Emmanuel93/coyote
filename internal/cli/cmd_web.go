package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/Emmanuel93/coyote/internal/ledger"
	"github.com/Emmanuel93/coyote/internal/usage"
	"github.com/Emmanuel93/coyote/internal/web"
)

// webServe arranca el servidor; las pruebas lo reemplazan.
var webServe = func(srv *http.Server, ln net.Listener) error { return srv.Serve(ln) }

func cmdWeb(a *app, args []string) error {
	fs := a.flags("web", "[--addr 127.0.0.1:7410]")
	addr := fs.String("addr", "127.0.0.1:7410", "dirección de escucha; solo loopback")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	if err := web.Loopback(*addr); err != nil {
		return fail(2, "%v", err)
	}
	root, cfg, err := a.project()
	if err != nil {
		return err
	}
	type source struct{ name, root string }
	sources := []source{{cfg.Name, root}}
	names := []string{cfg.Name}
	for _, r := range cfg.Repos {
		dir := ""
		if r.Path != "" {
			p := r.Path
			if !filepath.IsAbs(p) {
				p = filepath.Join(root, p)
			}
			dir = p
		} else if _, d := docsDir(root, r.Name); d != "" {
			dir = d
		}
		if dir == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "coyote", "ledger")); err == nil {
			sources = append(sources, source{r.Name, dir})
			names = append(names, r.Name)
		}
	}
	s := &web.Server{Title: cfg.Name, Sources: names, Now: a.now, Load: func() ([]usage.Event, error) {
		var all []usage.Event
		for _, src := range sources {
			entries, _, err := ledger.Open(src.root).ReadAll()
			if err != nil {
				return nil, err
			}
			all = append(all, usage.FromLedger(src.name, entries)...)
		}
		return all, nil
	}}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	fmt.Fprintf(a.stdout, "Costos en http://%s · %d fuentes (%v) · solo esta máquina · Ctrl+C para salir\n", ln.Addr(), len(sources), names)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	if err := webServe(srv, ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
