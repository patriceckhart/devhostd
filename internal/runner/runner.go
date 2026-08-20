package runner

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"time"

	"github.com/devhostd/devhostd/internal/control"
	"github.com/devhostd/devhostd/internal/routes"
	"github.com/devhostd/devhostd/internal/state"
)

type Options struct {
	Name    string
	AppPort int
	Force   bool
	Command []string
	Layout  state.Layout
	Info    state.DaemonInfo
}

func FreePort() (int, error) {
	for p := 4000; p < 5000; p++ {
		l, e := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if e == nil {
			l.Close()
			return p, nil
		}
	}
	return 0, errors.New("no free port in 4000-4999")
}
func Run(ctx context.Context, o Options) error {
	if len(o.Command) == 0 {
		return errors.New("no child command provided")
	}
	if os.Getenv("DEVHOSTD") == "0" {
		c := exec.CommandContext(ctx, o.Command[0], o.Command[1:]...)
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	}
	port := o.AppPort
	var e error
	if port == 0 {
		port, e = FreePort()
		if e != nil {
			return e
		}
	}
	r := routes.Route{Name: o.Name, Port: port, PID: os.Getpid()}
	if e = control.Call(ctx, o.Layout.Socket(), "register", map[string]any{"route": r, "force": o.Force}, nil); e != nil {
		return e
	}
	defer control.Call(context.Background(), o.Layout.Socket(), "deregister", map[string]any{"name": o.Name, "pid": os.Getpid()}, nil)
	scheme := "http"
	if o.Info.TLS {
		scheme = "https"
	}
	host := o.Name + "." + o.Info.TLDs[0]
	url := fmt.Sprintf("%s://%s", scheme, host)
	if o.Info.Port != 80 && o.Info.Port != 443 {
		url += ":" + strconv.Itoa(o.Info.Port)
	}
	fmt.Println("-> " + url)
	cmd := exec.Command(o.Command[0], o.Command[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "PORT="+strconv.Itoa(port), "HOST=127.0.0.1", "DEVHOSTD_URL="+url)
	if o.Info.TLS {
		cmd.Env = append(cmd.Env, "NODE_EXTRA_CA_CERTS="+o.Layout.CA()+"/rootCA.pem")
	}
	configure(cmd)
	if e = cmd.Start(); e != nil {
		return e
	}
	cleanup, e := started(cmd)
	if e != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return e
	}
	defer cleanup()
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt)
	defer signal.Stop(signals)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case e = <-done:
		return e
	case s := <-signals:
		forward(cmd, s)
		return waitForExit(cmd, done)
	case <-ctx.Done():
		stop(cmd)
		_ = waitForExit(cmd, done)
		return ctx.Err()
	}
}
func waitForExit(cmd *exec.Cmd, done <-chan error) error {
	select {
	case e := <-done:
		return e
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		return <-done
	}
}
