// Package netutil holds the one loopback check the settings file, the RPC listener and the
// login command share.
package netutil

import (
	"errors"
	"net"
	"strconv"
)

// IsLoopback reports whether host (no port) is "localhost" or a loopback IP literal.
func IsLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// CheckLoopbackAddr splits a loopback host:port. It returns the host as an IP literal
// ("localhost" becomes 127.0.0.1) and the port, which must be 0-65535.
func CheckLoopbackAddr(addr string) (host string, port int, err error) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, errors.New("must be a loopback host:port (127.0.0.1, ::1 or localhost)")
	}
	if !IsLoopback(h) {
		return "", 0, errors.New("must be a loopback address (127.0.0.1, ::1 or localhost), not " + strconv.Quote(h))
	}
	if h == "localhost" {
		h = "127.0.0.1"
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 0 || n > 65535 {
		return "", 0, errors.New("port must be a number between 0 and 65535")
	}
	return net.ParseIP(h).String(), n, nil
}
