//go:build !linux

package devrunner

import (
	"errors"
	"net"
)

var errRequiresLinux = errors.New("dev-runner requires Linux")

type peerListener struct {
	*net.UnixListener
	group uint32
}

// Accept refuses connections where Linux peer authentication is unavailable.
func (l peerListener) Accept() (net.Conn, error) {
	return nil, errRequiresLinux
}
