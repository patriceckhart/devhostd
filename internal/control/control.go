package control

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

const Version = 1

type Request struct {
	V      int             `json:"v"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}
type Response struct {
	V      int             `json:"v"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}
type Handler func(context.Context, Request) (any, error)

func Serve(ctx context.Context, ln net.Listener, h Handler) error {
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		c, e := ln.Accept()
		if e != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return e
			}
		}
		go serveConn(ctx, c, h)
	}
}
func serveConn(ctx context.Context, c net.Conn, h Handler) {
	defer c.Close()
	s := bufio.NewScanner(c)
	enc := json.NewEncoder(c)
	for s.Scan() {
		var q Request
		if e := json.Unmarshal(s.Bytes(), &q); e != nil {
			enc.Encode(Response{V: Version, Error: e.Error()})
			continue
		}
		if q.V != Version {
			enc.Encode(Response{V: Version, Error: "control protocol version mismatch; restart the daemon"})
			continue
		}
		v, e := h(ctx, q)
		r := Response{V: Version, OK: e == nil}
		if e != nil {
			r.Error = e.Error()
		} else if v != nil {
			r.Result, _ = json.Marshal(v)
		}
		if enc.Encode(r) != nil {
			return
		}
	}
}
func Call(ctx context.Context, address, method string, params, out any) error {
	d := net.Dialer{Timeout: 2 * time.Second}
	c, e := dial(ctx, d, address)
	if e != nil {
		return e
	}
	defer c.Close()
	raw, e := json.Marshal(params)
	if e != nil {
		return e
	}
	if e = json.NewEncoder(c).Encode(Request{V: Version, Method: method, Params: raw}); e != nil {
		return e
	}
	var r Response
	if e = json.NewDecoder(c).Decode(&r); e != nil {
		return e
	}
	if !r.OK {
		return fmt.Errorf("%s", r.Error)
	}
	if out != nil && len(r.Result) > 0 {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}
