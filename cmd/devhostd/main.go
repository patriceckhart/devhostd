package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/devhostd/devhostd/internal/ca"
	"github.com/devhostd/devhostd/internal/config"
	"github.com/devhostd/devhostd/internal/control"
	"github.com/devhostd/devhostd/internal/daemon"
	"github.com/devhostd/devhostd/internal/doctor"
	"github.com/devhostd/devhostd/internal/hosts"
	"github.com/devhostd/devhostd/internal/routes"
	"github.com/devhostd/devhostd/internal/runner"
	"github.com/devhostd/devhostd/internal/service"
	"github.com/devhostd/devhostd/internal/state"
	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	code := 0
	root := &cobra.Command{Use: "devhostd", DisableFlagParsing: true, SilenceErrors: true, SilenceUsage: true, Args: cobra.ArbitraryArgs, Run: func(cmd *cobra.Command, args []string) { code = run(args) }}
	root.SetArgs(os.Args[1:])
	if e := root.Execute(); e != nil {
		fmt.Fprintln(os.Stderr, "devhostd:", e)
		code = 1
	}
	os.Exit(code)
}
func run(args []string) int {
	l, e := state.Default()
	if e != nil {
		return fail(e, 1)
	}
	for i := 0; i < len(args); i++ {
		if args[i] == "--state-dir" && i+1 < len(args) {
			l, e = state.New(args[i+1])
			args = append(args[:i], args[i+2:]...)
			break
		}
	}
	if len(args) == 0 {
		usage(os.Stderr)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch args[0] {
	case "run":
		e = runApp(ctx, l, args[1:])
	case "daemon":
		e = daemonCmd(ctx, l, args[1:])
	case "alias":
		e = aliasCmd(ctx, l, args[1:])
	case "list":
		e = listCmd(ctx, l, args[1:])
	case "status":
		e = statusCmd(ctx, l, args[1:])
	case "prune":
		var x map[string]int
		e = control.Call(ctx, l.Socket(), "prune", nil, &x)
		if e == nil {
			fmt.Printf("removed %d stale route(s)\n", x["removed"])
		}
	case "doctor":
		e = doctorCmd(l, args[1:])
	case "hosts":
		e = hostsCmd(ctx, l, args[1:])
	case "trust":
		e = trust(l)
	case "clean":
		e = clean(ctx, l)
	case "service":
		e = serviceCmd(l, args[1:])
	case "share":
		e = shareCmd(ctx, l, args[1:])
	case "api":
		e = apiCmd(ctx, l, args[1:])
	case "version", "--version", "-v":
		fmt.Println(version)
		return 0
	case "help", "--help", "-h":
		usage(os.Stdout)
		return 0
	default:
		e = fmt.Errorf("unknown command %q", args[0])
	}
	if e != nil {
		var exitErr *exec.ExitError
		if errors.As(e, &exitErr) {
			return exitErr.ExitCode()
		}
		return fail(e, 1)
	}
	return 0
}
func fail(e error, code int) int { fmt.Fprintln(os.Stderr, "devhostd:", e); return code }
func usage(w io.Writer) {
	fmt.Fprintln(w, `Usage: devhostd <command> [options]

Commands:
  run [name] -- <command>   Run an app behind the local proxy
  alias <name> <port>       Register a static route
  list [--json]             List routes
  status [--json]           Show daemon status
  daemon start|stop         Manage the daemon
  trust                     Trust the generated local CA
  hosts sync|clean          Manage the hosts file block
  doctor [--json]           Run diagnostics
  prune                     Remove stale routes
  clean                     Remove local state
  share tailscale|ngrok     Publish a route
  api <method> [json]       Call the agent-facing control API
  version                   Print version

Global option: --state-dir <path>`)
}
func daemonCmd(ctx context.Context, l state.Layout, args []string) error {
	if len(args) == 0 {
		return errors.New("daemon requires start or stop")
	}
	if args[0] == "stop" {
		return control.Call(ctx, l.Socket(), "stop", nil, nil)
	}
	if args[0] != "start" {
		return errors.New("daemon requires start or stop")
	}
	fs := flag.NewFlagSet("daemon start", flag.ContinueOnError)
	port := fs.Int("port", envInt("DEVHOSTD_PORT", 0), "proxy port")
	fs.IntVar(port, "p", *port, "proxy port")
	noTLS := fs.Bool("no-tls", os.Getenv("DEVHOSTD_HTTPS") == "0", "disable TLS")
	foreground := fs.Bool("foreground", false, "stay in foreground")
	wild := fs.Bool("wildcard", os.Getenv("DEVHOSTD_WILDCARD") == "1", "wildcard matching")
	lan := fs.Bool("lan", os.Getenv("DEVHOSTD_LAN") == "1", "listen on the LAN and publish .local routes")
	cert := fs.String("cert", "", "certificate path")
	key := fs.String("key", "", "private key path")
	var tlds stringsFlag
	if x := os.Getenv("DEVHOSTD_TLD"); x != "" {
		tlds = strings.Split(x, ",")
	}
	fs.Var(&tlds, "tld", "hostname suffix")
	if e := fs.Parse(args[1:]); e != nil {
		return e
	}
	if len(tlds) == 0 {
		tlds = []string{"localhost"}
	}
	opt := daemon.Options{Layout: l, Version: version, Port: *port, TLS: !*noTLS, TLDs: tlds, Wildcard: *wild, LAN: *lan, CertFile: *cert, KeyFile: *key}
	effectivePort := opt.Port
	if effectivePort == 0 {
		if opt.TLS {
			effectivePort = 443
		} else {
			effectivePort = 80
		}
	}
	if effectivePort < 1024 && !elevated() {
		return elevate(append([]string{"daemon"}, append(args, "--state-dir", l.Root)...))
	}
	if err := state.WriteJSON(l.Config(), state.DaemonConfig{Port: opt.Port, TLS: opt.TLS, TLDs: opt.TLDs, Wildcard: opt.Wildcard, LAN: opt.LAN, CertFile: opt.CertFile, KeyFile: opt.KeyFile}, 0600); err != nil {
		return err
	}
	if *foreground {
		return daemon.Run(ctx, opt)
	}
	child := []string{"daemon", "start", "--foreground", "--port", strconv.Itoa(*port)}
	if *noTLS {
		child = append(child, "--no-tls")
	}
	if *wild {
		child = append(child, "--wildcard")
	}
	if *lan {
		child = append(child, "--lan")
	}
	for _, t := range tlds {
		child = append(child, "--tld", t)
	}
	if *cert != "" {
		child = append(child, "--cert", *cert, "--key", *key)
	}
	return spawnDaemon(ctx, l, child)
}
func spawnDaemon(ctx context.Context, l state.Layout, args []string) error {
	if control.Call(ctx, l.Socket(), "ping", nil, nil) == nil {
		return nil
	}
	if e := l.Ensure(); e != nil {
		return e
	}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	logPath := filepath.Join(l.Root, "logs", "daemon.log")
	rotateLog(logPath)
	log, e := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer log.Close()
	args = append(args, "--state-dir", l.Root)
	cmd := exec.Command(exe, args...)
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.Stdin = nil
	if e = cmd.Start(); e != nil {
		return e
	}
	_ = cmd.Process.Release()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if e = control.Call(ctx, l.Socket(), "ping", nil, nil); e == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not start; see %s", logPath)
}

type stringsFlag []string

func (s *stringsFlag) String() string     { return strings.Join(*s, ",") }
func (s *stringsFlag) Set(v string) error { *s = append(*s, v); return nil }
func ensureDaemon(ctx context.Context, l state.Layout) (state.DaemonInfo, error) {
	var i state.DaemonInfo
	if control.Call(ctx, l.Socket(), "status", nil, &i) == nil {
		return i, nil
	}
	cfg := state.DaemonConfig{TLS: true, TLDs: []string{"localhost"}}
	_ = state.ReadJSON(l.Config(), &cfg)
	port, tls := cfg.Port, cfg.TLS
	if os.Getenv("DEVHOSTD_PORT") != "" {
		port = envInt("DEVHOSTD_PORT", port)
	}
	if os.Getenv("DEVHOSTD_HTTPS") != "" {
		tls = os.Getenv("DEVHOSTD_HTTPS") != "0"
	}
	args := []string{"daemon", "start", "--foreground", "--port", strconv.Itoa(port)}
	if !tls {
		args = append(args, "--no-tls")
	}
	tlds := cfg.TLDs
	if x := os.Getenv("DEVHOSTD_TLD"); x != "" {
		tlds = strings.Split(x, ",")
	}
	for _, t := range tlds {
		args = append(args, "--tld", t)
	}
	if cfg.Wildcard || os.Getenv("DEVHOSTD_WILDCARD") == "1" {
		args = append(args, "--wildcard")
	}
	if cfg.LAN || os.Getenv("DEVHOSTD_LAN") == "1" {
		args = append(args, "--lan")
	}
	if cfg.CertFile != "" {
		args = append(args, "--cert", cfg.CertFile, "--key", cfg.KeyFile)
	}
	effectivePort := port
	if effectivePort == 0 {
		if tls {
			effectivePort = 443
		} else {
			effectivePort = 80
		}
	}
	if effectivePort < 1024 && !elevated() {
		startArgs := append([]string{}, args...)
		startArgs = append(startArgs, "--state-dir", l.Root)
		if e := elevate(startArgs); e != nil {
			return i, e
		}
	} else if e := spawnDaemon(ctx, l, args); e != nil {
		return i, e
	}
	e := control.Call(ctx, l.Socket(), "status", nil, &i)
	return i, e
}
func runApp(ctx context.Context, l state.Layout, args []string) error {
	name := ""
	nameBypass := false
	port := envInt("DEVHOSTD_APP_PORT", 0)
	force := false
	cmd := []string{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--":
			cmd = args[i+1:]
			i = len(args)
		case "--name":
			if i+1 >= len(args) {
				return errors.New("--name requires a value")
			}
			name = args[i+1]
			nameBypass = true
			i++
		case "--app-port":
			if i+1 >= len(args) {
				return errors.New("--app-port requires a value")
			}
			port, _ = strconv.Atoi(args[i+1])
			i++
		case "--force":
			force = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown run option %s", args[i])
			}
			if name == "" {
				name = args[i]
			} else {
				return errors.New("use -- before the child command")
			}
		}
	}
	cwd, _ := os.Getwd()
	cfg, _, _ := config.Find(cwd)
	if name == "" {
		name = cfg.Name
	}
	if name == "" {
		name = inferName(cwd)
	}
	if port == 0 {
		port = cfg.AppPort
	}
	if e := routes.ValidateName(name); e != nil {
		return e
	}
	if !nameBypass && reservedName(name) {
		return fmt.Errorf("%q is reserved; use --name to select it explicitly", name)
	}
	if len(cmd) == 0 {
		var e error
		cmd, e = defaultCommand(cwd, cfg.Script)
		if e != nil {
			return e
		}
	}
	info, e := ensureDaemon(ctx, l)
	if e != nil {
		return e
	}
	if info.TLS {
		promptTrust(l)
	}
	return runner.Run(ctx, runner.Options{Name: name, AppPort: port, Force: force, Command: cmd, Layout: l, Info: info})
}
func reservedName(name string) bool {
	switch name {
	case "run", "daemon", "alias", "list", "trust", "doctor", "clean", "hosts", "service", "prune", "status":
		return true
	}
	return false
}
func inferName(cwd string) string {
	root := gitOutput(cwd, "rev-parse", "--show-toplevel")
	if root == "" {
		root = cwd
	}
	name := dnsSlug(filepath.Base(root))
	gitDir := gitOutput(cwd, "rev-parse", "--absolute-git-dir")
	common := gitOutput(cwd, "rev-parse", "--git-common-dir")
	if gitDir != "" && common != "" {
		if !filepath.IsAbs(common) {
			common = filepath.Join(cwd, common)
		}
		if filepath.Clean(gitDir) != filepath.Clean(common) {
			if branch := dnsSlug(gitOutput(cwd, "branch", "--show-current")); branch != "" {
				name = branch + "." + name
			}
		}
	}
	return name
}
func dnsSlug(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	dash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
func gitOutput(dir string, args ...string) string {
	c := exec.Command("git", args...)
	c.Dir = dir
	b, e := c.Output()
	if e != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
func defaultCommand(cwd, script string) ([]string, error) {
	if _, e := os.Stat(filepath.Join(cwd, "package.json")); e != nil {
		return nil, errors.New("no command provided and no package.json found")
	}
	if script == "" {
		script = "dev"
	}
	manager := "bun"
	if exists(cwd, "package-lock.json") {
		manager = "npm"
	} else if exists(cwd, "yarn.lock") {
		manager = "yarn"
	} else if exists(cwd, "pnpm-lock.yaml") {
		manager = "pnpm"
	} else if exists(cwd, "bun.lock") {
		manager = "bun"
	}
	return []string{manager, "run", script}, nil
}
func exists(dir, name string) bool { _, e := os.Stat(filepath.Join(dir, name)); return e == nil }
func aliasCmd(ctx context.Context, l state.Layout, args []string) error {
	force := false
	remove := ""
	pos := []string{}
	for i := 0; i < len(args); i++ {
		if args[i] == "--force" {
			force = true
		} else if args[i] == "--remove" && i+1 < len(args) {
			remove = args[i+1]
			i++
		} else {
			pos = append(pos, args[i])
		}
	}
	daemonUp := control.Call(ctx, l.Socket(), "ping", nil, nil) == nil
	if remove != "" {
		if daemonUp {
			return control.Call(ctx, l.Socket(), "deregister", map[string]any{"name": remove, "pid": 0}, nil)
		}
		var saved []routes.Route
		_ = state.ReadJSON(l.Routes(), &saved)
		out := saved[:0]
		for _, r := range saved {
			if r.Name != remove {
				out = append(out, r)
			}
		}
		return state.WriteJSON(l.Routes(), out, 0600)
	}
	if len(pos) != 2 {
		return errors.New("usage: devhostd alias <name> <port>")
	}
	port, e := strconv.Atoi(pos[1])
	if e != nil {
		return e
	}
	route := routes.Route{Name: pos[0], Port: port, Static: true}
	if daemonUp {
		return control.Call(ctx, l.Socket(), "register", map[string]any{"route": route, "force": force}, nil)
	}
	cfg := state.DaemonConfig{TLDs: []string{"localhost"}}
	_ = state.ReadJSON(l.Config(), &cfg)
	table := routes.New(cfg.TLDs, cfg.Wildcard)
	var saved []routes.Route
	_ = state.ReadJSON(l.Routes(), &saved)
	table.Replace(saved)
	if e = table.Register(route, force, nil); e != nil {
		return e
	}
	if e = l.Ensure(); e != nil {
		return e
	}
	return state.WriteJSON(l.Routes(), table.List(), 0600)
}
func listCmd(ctx context.Context, l state.Layout, args []string) error {
	var rs []routes.Route
	if e := control.Call(ctx, l.Socket(), "list", nil, &rs); e != nil {
		return e
	}
	jsonOut := contains(args, "--json")
	if jsonOut {
		return printJSON(rs)
	}
	info, _ := state.LoadInfo(l)
	scheme := "http"
	if info.TLS {
		scheme = "https"
	}
	for _, r := range rs {
		url := scheme + "://" + r.Hostnames[0]
		if info.Port != 80 && info.Port != 443 {
			url += ":" + strconv.Itoa(info.Port)
		}
		kind := "live"
		if r.Static {
			kind = "alias"
		}
		fmt.Printf("%-24s %-40s -> 127.0.0.1:%d  %s\n", r.Name, url, r.Port, kind)
	}
	return nil
}
func statusCmd(ctx context.Context, l state.Layout, args []string) error {
	var i state.DaemonInfo
	if e := control.Call(ctx, l.Socket(), "status", nil, &i); e != nil {
		return e
	}
	if contains(args, "--json") {
		return printJSON(i)
	}
	fmt.Printf("running (pid %d)\nproxy: port %d, tls=%t, lan=%t\ntlds: %s\nstarted: %s\n", i.PID, i.Port, i.TLS, i.LAN, strings.Join(i.TLDs, ", "), i.StartedAt)
	return nil
}
func doctorCmd(l state.Layout, args []string) error {
	c := doctor.Run(l, version)
	if contains(args, "--json") {
		return printJSON(c)
	}
	for _, x := range c {
		fmt.Printf("%-4s %-16s %s\n", x.Status, x.Name, x.Message)
	}
	return nil
}
func hostsCmd(ctx context.Context, l state.Layout, args []string) error {
	if len(args) != 1 {
		return errors.New("hosts requires sync or clean")
	}
	var rs []routes.Route
	if args[0] == "sync" {
		if e := control.Call(ctx, l.Socket(), "list", nil, &rs); e != nil {
			return e
		}
	} else if args[0] != "clean" {
		return errors.New("hosts requires sync or clean")
	}
	return hosts.Sync(hosts.Path(), rs)
}
func apiCmd(ctx context.Context, l state.Layout, args []string) error {
	if len(args) == 0 {
		return errors.New("api requires a control method")
	}
	params := json.RawMessage(`{}`)
	if len(args) > 1 {
		params = json.RawMessage(args[1])
		if !json.Valid(params) {
			return errors.New("API parameters must be valid JSON")
		}
	}
	var result json.RawMessage
	if e := control.Call(ctx, l.Socket(), args[0], params, &result); e != nil {
		return e
	}
	if len(result) == 0 {
		fmt.Println("null")
		return nil
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, result, "", "  ") == nil {
		fmt.Println(pretty.String())
	} else {
		fmt.Println(string(result))
	}
	return nil
}
func shareCmd(ctx context.Context, l state.Layout, args []string) error {
	if len(args) < 2 {
		return errors.New("usage: devhostd share tailscale|ngrok <route> [--public]")
	}
	provider, name := args[0], args[1]
	var route routes.Route
	if e := control.Call(ctx, l.Socket(), "route", map[string]string{"name": name}, &route); e != nil {
		return e
	}
	var info state.DaemonInfo
	if e := control.Call(ctx, l.Socket(), "status", nil, &info); e != nil {
		return e
	}
	scheme := "http"
	targetScheme := "http"
	if info.TLS {
		scheme = "https"
		targetScheme = "https+insecure"
	}
	target := fmt.Sprintf("%s://127.0.0.1:%d", targetScheme, info.Port)
	switch provider {
	case "tailscale":
		public := contains(args, "--public")
		sub := "serve"
		if public {
			sub = "funnel"
		}
		b, e := exec.Command("tailscale", "status", "--json").Output()
		if e != nil {
			return e
		}
		var x struct {
			Self struct {
				DNSName string `json:"DNSName"`
			} `json:"Self"`
		}
		if e = json.Unmarshal(b, &x); e != nil {
			return e
		}
		host := strings.TrimSuffix(x.Self.DNSName, ".")
		if host == "" {
			return errors.New("tailscale status did not report a DNS name")
		}
		if contains(args, "--stop") {
			cmd := exec.Command("tailscale", sub, "--https=443", "off")
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			if e = cmd.Run(); e != nil {
				return e
			}
			_ = control.Call(ctx, l.Socket(), "remove_hostname", map[string]string{"name": name, "hostname": host}, nil)
			return nil
		}
		cmd := exec.Command("tailscale", sub, "--bg", "--https=443", target)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if e = cmd.Run(); e != nil {
			return e
		}
		if e = control.Call(ctx, l.Socket(), "add_hostname", map[string]string{"name": name, "hostname": host}, nil); e != nil {
			return e
		}
		fmt.Printf("-> %s://%s\n", scheme, host)
		return nil
	case "ngrok":
		ngrokScheme := "http"
		if info.TLS {
			ngrokScheme = "https"
		}
		ngrokTarget := fmt.Sprintf("%s://127.0.0.1:%d", ngrokScheme, info.Port)
		cmdArgs := []string{"http", ngrokTarget}
		if info.TLS {
			cmdArgs = append(cmdArgs, "--upstream-tls-verify=false")
		}
		cmd := exec.Command("ngrok", cmdArgs...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if e := cmd.Start(); e != nil {
			return e
		}
		publicURL, e := waitForNgrok(ctx, 10*time.Second)
		if e != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return e
		}
		parsed, e := url.Parse(publicURL)
		if e != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return e
		}
		if e = control.Call(ctx, l.Socket(), "add_hostname", map[string]string{"name": name, "hostname": parsed.Hostname()}, nil); e != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return e
		}
		fmt.Println("-> " + publicURL)
		return cmd.Wait()
	default:
		return fmt.Errorf("unknown sharing provider %q", provider)
	}
}
func waitForNgrok(ctx context.Context, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:4040/api/tunnels", nil)
		if resp, e := http.DefaultClient.Do(req); e == nil {
			var x struct {
				Tunnels []struct {
					PublicURL string `json:"public_url"`
				} `json:"tunnels"`
			}
			e = json.NewDecoder(resp.Body).Decode(&x)
			resp.Body.Close()
			if e == nil && len(x.Tunnels) > 0 {
				return x.Tunnels[0].PublicURL, nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "", errors.New("ngrok did not report a public URL")
}
func serviceCmd(l state.Layout, args []string) error {
	if len(args) == 0 {
		return errors.New("service requires install, status, or uninstall")
	}
	switch args[0] {
	case "install":
		exe, e := os.Executable()
		if e != nil {
			return e
		}
		return service.Install(exe, l.Root, args[1:])
	case "status":
		s := service.GetStatus()
		if contains(args, "--json") {
			return printJSON(s)
		}
		fmt.Printf("%s: installed=%t (%s)\n", s.Manager, s.Installed, s.Definition)
		return nil
	case "uninstall":
		return service.Uninstall()
	default:
		return errors.New("service requires install, status, or uninstall")
	}
}
func trust(l state.Layout) error {
	root := filepath.Join(l.CA(), "rootCA.pem")
	if _, e := os.Stat(root); e != nil {
		return errors.New("CA not found; start the TLS daemon first")
	}
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("sudo", "security", "add-trusted-cert", "-d", "-r", "trustRoot", "-k", "/Library/Keychains/System.keychain", root)
	case "windows":
		c = exec.Command("certutil", "-addstore", "-f", "Root", root)
	case "linux":
		if _, e := exec.LookPath("update-ca-certificates"); e == nil {
			if e = exec.Command("sudo", "install", "-m", "0644", root, "/usr/local/share/ca-certificates/devhostd.crt").Run(); e != nil {
				return e
			}
			c = exec.Command("sudo", "update-ca-certificates")
		} else if _, e := exec.LookPath("update-ca-trust"); e == nil {
			if e = exec.Command("sudo", "install", "-m", "0644", root, "/etc/pki/ca-trust/source/anchors/devhostd.pem").Run(); e != nil {
				return e
			}
			c = exec.Command("sudo", "update-ca-trust")
		} else if _, e := exec.LookPath("trust"); e == nil {
			c = exec.Command("sudo", "trust", "anchor", root)
		} else {
			return errors.New("no supported system trust-store tool found")
		}
	default:
		return errors.New("system trust installation is unsupported on this platform")
	}
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Stdin = os.Stdin
	if e := c.Run(); e != nil {
		return e
	}
	return state.AtomicWrite(filepath.Join(l.CA(), "trusted"), []byte(time.Now().Format(time.RFC3339)+"\n"), 0600)
}
func promptTrust(l state.Layout) {
	if _, e := os.Stat(filepath.Join(l.CA(), "trusted")); e == nil {
		return
	}
	if os.Getenv("CI") == "1" {
		return
	}
	info, e := os.Stdin.Stat()
	if e != nil || info.Mode()&os.ModeCharDevice == 0 {
		return
	}
	fmt.Print("Trust the devhostd local CA now? [Y/n] ")
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer == "" || answer == "y" || answer == "yes" {
		if e := trust(l); e != nil {
			fmt.Fprintln(os.Stderr, "devhostd: CA trust failed:", e)
		}
	}
}
func clean(ctx context.Context, l state.Layout) error {
	_ = control.Call(ctx, l.Socket(), "stop", nil, nil)
	time.Sleep(100 * time.Millisecond)
	if service.GetStatus().Installed {
		if e := service.Uninstall(); e != nil {
			return e
		}
	}
	if _, e := os.Stat(filepath.Join(l.CA(), "trusted")); e == nil {
		if e = untrust(l); e != nil {
			return fmt.Errorf("remove CA trust: %w", e)
		}
	}
	if e := hosts.Sync(hosts.Path(), nil); e != nil && !os.IsPermission(e) {
		return e
	}
	return state.Remove(l)
}
func untrust(l state.Layout) error {
	root := filepath.Join(l.CA(), "rootCA.pem")
	name, e := ca.RootCommonName(root)
	if e != nil {
		return e
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("sudo", "security", "delete-certificate", "-c", name, "/Library/Keychains/System.keychain").Run()
	case "windows":
		return exec.Command("certutil", "-delstore", "Root", name).Run()
	case "linux":
		if _, e = os.Stat("/usr/local/share/ca-certificates/devhostd.crt"); e == nil {
			if e = exec.Command("sudo", "rm", "-f", "/usr/local/share/ca-certificates/devhostd.crt").Run(); e != nil {
				return e
			}
			return exec.Command("sudo", "update-ca-certificates").Run()
		}
		if _, e = os.Stat("/etc/pki/ca-trust/source/anchors/devhostd.pem"); e == nil {
			if e = exec.Command("sudo", "rm", "-f", "/etc/pki/ca-trust/source/anchors/devhostd.pem").Run(); e != nil {
				return e
			}
			return exec.Command("sudo", "update-ca-trust").Run()
		}
		if _, e = exec.LookPath("trust"); e == nil {
			return exec.Command("sudo", "trust", "anchor", "--remove", root).Run()
		}
	}
	return nil
}
func printJSON(v any) error {
	e := json.NewEncoder(os.Stdout)
	e.SetIndent("", "  ")
	return e.Encode(v)
}
func contains(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}
func envInt(k string, d int) int {
	if n, e := strconv.Atoi(os.Getenv(k)); e == nil && n >= 0 {
		return n
	}
	return d
}
func rotateLog(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < 5<<20 {
		return
	}
	_ = os.Remove(path + ".3")
	_ = os.Rename(path+".2", path+".3")
	_ = os.Rename(path+".1", path+".2")
	_ = os.Rename(path, path+".1")
}
