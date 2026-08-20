package daemon

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/devhostd/devhostd/internal/ca"
	"github.com/devhostd/devhostd/internal/control"
	"github.com/devhostd/devhostd/internal/hosts"
	devmdns "github.com/devhostd/devhostd/internal/mdns"
	"github.com/devhostd/devhostd/internal/routes"
	"github.com/devhostd/devhostd/internal/state"
	"golang.org/x/net/http2"
)

type Options struct {
	Layout   state.Layout
	Version  string
	Port     int
	TLS      bool
	TLDs     []string
	Wildcard bool
	LAN      bool
	CertFile string
	KeyFile  string
}
type Server struct {
	opt     Options
	table   *routes.Table
	started time.Time
	cancel  context.CancelFunc
}

func Run(ctx context.Context, o Options) error {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	if o.Port == 0 {
		if o.TLS {
			o.Port = 443
		} else {
			o.Port = 80
		}
	}
	if len(o.TLDs) == 0 {
		o.TLDs = []string{"localhost"}
	}
	if o.LAN {
		found := false
		for _, t := range o.TLDs {
			if t == "local" {
				found = true
			}
		}
		if !found {
			o.TLDs = append(o.TLDs, "local")
		}
	}
	if err := o.Layout.Ensure(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &Server{opt: o, table: routes.New(o.TLDs, o.Wildcard), started: time.Now().UTC(), cancel: cancel}
	var saved []routes.Route
	if state.ReadJSON(o.Layout.Routes(), &saved) == nil {
		for i := range saved {
			for _, host := range s.table.Expand(saved[i].Name) {
				found := false
				for _, existing := range saved[i].Hostnames {
					if existing == host {
						found = true
					}
				}
				if !found {
					saved[i].Hostnames = append(saved[i].Hostnames, host)
				}
			}
		}
		s.table.Replace(saved)
	}
	if o.LAN {
		manager, e := devmdns.Start(s.table)
		if e != nil {
			return fmt.Errorf("start mDNS: %w", e)
		}
		defer manager.Close()
	}
	bind4 := "127.0.0.1"
	if o.LAN {
		bind4 = "0.0.0.0"
	}
	ln, e := net.Listen("tcp4", fmt.Sprintf("%s:%d", bind4, o.Port))
	if e != nil {
		return fmt.Errorf("bind proxy port %d: %w", o.Port, e)
	}
	defer ln.Close()
	cln, e := control.Listen(o.Layout.Socket())
	if e != nil {
		return fmt.Errorf("control listener: %w", e)
	}
	defer cln.Close()
	_ = state.ChownInvokingUser(o.Layout.Socket())
	defer os.Remove(o.Layout.Socket())
	info := state.DaemonInfo{Version: o.Version, PID: os.Getpid(), Port: o.Port, TLS: o.TLS, LAN: o.LAN, TLDs: o.TLDs, Socket: o.Layout.Socket(), StartedAt: s.started.Format(time.RFC3339)}
	if e = state.WriteJSON(o.Layout.Info(), info, 0600); e != nil {
		return e
	}
	_ = state.ChownTreeInvokingUser(o.Layout.Root)
	defer os.Remove(o.Layout.Info())
	if o.TLS && o.Port == 443 {
		go serveRedirect(ctx)
	}
	go func() {
		if e := control.Serve(ctx, cln, s.handle); e != nil {
			slog.Error("control server stopped", "error", e)
			cancel()
		}
	}()
	httpServer := &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	if o.TLS {
		if err := http2.ConfigureServer(httpServer, &http2.Server{}); err != nil {
			return fmt.Errorf("configure HTTP/2: %w", err)
		}
	}
	go func() {
		<-ctx.Done()
		shutdown, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		httpServer.Shutdown(shutdown)
	}()
	var tlsCfg *tls.Config
	if o.TLS {
		if o.CertFile != "" {
			cert, e := tls.LoadX509KeyPair(o.CertFile, o.KeyFile)
			if e != nil {
				return e
			}
			tlsCfg = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}}
		} else {
			auth, e := ca.OpenWithCerts(o.Layout.CA(), o.Layout.Certs())
			if e != nil {
				return e
			}
			tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}, GetCertificate: func(hi *tls.ClientHelloInfo) (*tls.Certificate, error) {
				host := hi.ServerName
				if host == "" {
					host = "devhostd.localhost"
				}
				if o.Wildcard {
					if route, ok := s.table.Lookup(host); ok {
						for _, base := range route.Hostnames {
							if host == base || strings.HasSuffix(host, "."+base) {
								return auth.Certificate(base, true)
							}
						}
					}
				}
				return auth.Certificate(host, false)
			}}
		}
		ln = tls.NewListener(ln, tlsCfg)
	}
	bind6 := "::1"
	if o.LAN {
		bind6 = "::"
	}
	if ln6, err := net.Listen("tcp6", fmt.Sprintf("[%s]:%d", bind6, o.Port)); err == nil {
		defer ln6.Close()
		var listener net.Listener = ln6
		if o.TLS {
			listener = tls.NewListener(ln6, tlsCfg)
		}
		go func() {
			if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Warn("IPv6 listener stopped", "error", err)
			}
		}()
	}
	e = httpServer.Serve(ln)
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
func serveRedirect(ctx context.Context) {
	srv := &http.Server{Addr: "127.0.0.1:80", ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusPermanentRedirect)
	})}
	go func() { <-ctx.Done(); _ = srv.Close() }()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Warn("HTTP redirect listener unavailable", "error", err)
	}
}
func (s *Server) persist() error {
	rs := s.table.List()
	if err := state.WriteJSON(s.opt.Layout.Routes(), rs, 0600); err != nil {
		return err
	}
	_ = state.ChownInvokingUser(s.opt.Layout.Routes())
	if os.Getenv("DEVHOSTD_SYNC_HOSTS") != "0" {
		if err := hosts.Sync(hosts.Path(), rs); err != nil {
			slog.Warn("hosts sync failed", "error", err)
		}
	}
	return nil
}
func (s *Server) handle(ctx context.Context, q control.Request) (any, error) {
	switch q.Method {
	case "ping":
		return map[string]any{"version": control.Version}, nil
	case "status":
		return state.DaemonInfo{Version: s.opt.Version, PID: os.Getpid(), Port: s.opt.Port, TLS: s.opt.TLS, LAN: s.opt.LAN, TLDs: s.opt.TLDs, Socket: s.opt.Layout.Socket(), StartedAt: s.started.Format(time.RFC3339)}, nil
	case "list", "routes":
		return s.table.List(), nil
	case "route":
		var p struct {
			Name string `json:"name"`
		}
		if e := json.Unmarshal(q.Params, &p); e != nil {
			return nil, e
		}
		for _, r := range s.table.List() {
			if r.Name == p.Name {
				return r, nil
			}
		}
		return nil, fmt.Errorf("route %q not found", p.Name)
	case "config":
		return state.DaemonConfig{Port: s.opt.Port, TLS: s.opt.TLS, TLDs: s.opt.TLDs, Wildcard: s.opt.Wildcard, LAN: s.opt.LAN, CertFile: s.opt.CertFile, KeyFile: s.opt.KeyFile}, nil
	case "health":
		checks := map[string]bool{}
		for _, r := range s.table.List() {
			checks[r.Name] = portOpen(r.Port)
		}
		return map[string]any{"ok": true, "routes": checks, "uptime_seconds": int(time.Since(s.started).Seconds())}, nil
	case "add_hostname", "remove_hostname":
		var p struct {
			Name     string `json:"name"`
			Hostname string `json:"hostname"`
		}
		if e := json.Unmarshal(q.Params, &p); e != nil {
			return nil, e
		}
		var e error
		if q.Method == "add_hostname" {
			e = s.table.AddHostname(p.Name, p.Hostname)
		} else {
			e = s.table.RemoveHostname(p.Name, p.Hostname)
		}
		if e != nil {
			return nil, e
		}
		return nil, s.persist()
	case "register":
		var p struct {
			Route routes.Route `json:"route"`
			Force bool         `json:"force"`
		}
		if e := json.Unmarshal(q.Params, &p); e != nil {
			return nil, e
		}
		if p.Force {
			for _, old := range s.table.List() {
				if old.Name == p.Route.Name && old.PID > 0 && old.PID != p.Route.PID {
					_ = takeover(old.PID)
				}
			}
		}
		if e := s.table.Register(p.Route, p.Force, alive); e != nil {
			return nil, e
		}
		return nil, s.persist()
	case "deregister":
		var p struct {
			Name string `json:"name"`
			PID  int    `json:"pid"`
		}
		if e := json.Unmarshal(q.Params, &p); e != nil {
			return nil, e
		}
		s.table.Remove(p.Name, p.PID)
		return nil, s.persist()
	case "stop":
		go s.cancel()
		return nil, nil
	case "prune":
		n := 0
		for _, r := range s.table.List() {
			if !r.Static && (!alive(r.PID) || !portOpen(r.Port)) {
				if alive(r.PID) {
					_ = takeover(r.PID)
				}
				if s.table.Remove(r.Name, r.PID) {
					n++
				}
			}
		}
		return map[string]int{"removed": n}, s.persist()
	default:
		return nil, fmt.Errorf("unknown control method %q", q.Method)
	}
}
func portOpen(port int) bool {
	c, e := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 150*time.Millisecond)
	if e == nil {
		c.Close()
		return true
	}
	return false
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route, ok := s.table.Lookup(r.Host)
	if !ok {
		s.unknown(w, r)
		return
	}
	depth, _ := strconv.Atoi(r.Header.Get("Devhostd-Depth"))
	if depth >= 5 {
		http.Error(w, "Loop detected. Check the development server proxy Host configuration.", http.StatusLoopDetected)
		return
	}
	if r.ProtoMajor == 2 && r.Method == http.MethodConnect {
		s.bridgeH2WebSocket(w, r, route)
		return
	}
	target := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", route.Port)}
	p := httputil.NewSingleHostReverseProxy(target)
	p.FlushInterval = -1
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		w.Header().Set("Retry-After", "1")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "<!doctype html><meta http-equiv=refresh content=1><title>App starting</title><p>The app is not ready. Retrying...</p>")
	}
	base := p.Director
	p.Director = func(req *http.Request) {
		host := req.Host
		base(req)
		req.Host = host
		req.Header.Set("X-Forwarded-Host", host)
		if s.opt.TLS {
			req.Header.Set("X-Forwarded-Proto", "https")
		} else {
			req.Header.Set("X-Forwarded-Proto", "http")
		}
		req.Header.Set("Devhostd-Depth", strconv.Itoa(depth+1))
	}
	p.ServeHTTP(w, r)
}
func (s *Server) bridgeH2WebSocket(w http.ResponseWriter, r *http.Request, route routes.Route) {
	conn, e := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", route.Port), 3*time.Second)
	if e != nil {
		http.Error(w, e.Error(), http.StatusBadGateway)
		return
	}
	defer conn.Close()
	path := r.URL.RequestURI()
	if path == "" {
		path = "/"
	}
	fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n", path, r.Host)
	for k, values := range r.Header {
		if strings.EqualFold(k, "Connection") || strings.EqualFold(k, "Upgrade") {
			continue
		}
		for _, v := range values {
			fmt.Fprintf(conn, "%s: %s\r\n", k, v)
		}
	}
	io.WriteString(conn, "\r\n")
	br := bufio.NewReader(conn)
	resp, e := http.ReadResponse(br, r)
	if e != nil {
		http.Error(w, e.Error(), http.StatusBadGateway)
		return
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		http.Error(w, "upstream rejected WebSocket upgrade", http.StatusBadGateway)
		return
	}
	for k, v := range resp.Header {
		for _, x := range v {
			w.Header().Add(k, x)
		}
	}
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	_ = http.NewResponseController(w).EnableFullDuplex()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(conn, r.Body); done <- struct{}{} }()
	go func() { _, _ = io.Copy(w, br); done <- struct{}{} }()
	<-done
}
func (s *Server) unknown(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	io.WriteString(w, "<!doctype html><title>Route not found</title><h1>No route for "+htmlEscape(r.Host)+"</h1><p>Register one with <code>devhostd run &lt;name&gt; -- &lt;command&gt;</code>.</p><ul>")
	for _, x := range s.table.List() {
		for _, h := range x.Hostnames {
			io.WriteString(w, "<li>"+htmlEscape(h)+"</li>")
		}
	}
	io.WriteString(w, "</ul>")
}
func htmlEscape(v string) string {
	v = strings.ReplaceAll(v, "&", "&amp;")
	v = strings.ReplaceAll(v, "<", "&lt;")
	return strings.ReplaceAll(v, ">", "&gt;")
}
