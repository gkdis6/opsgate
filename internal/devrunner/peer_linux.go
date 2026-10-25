package devrunner

import (
	"net"
	"os"
	"os/user"
	"strconv"

	"golang.org/x/sys/unix"
)

// Check both the connection's actual primary group and the account's group
// database. Failure to resolve supplementary membership denies access.
func allowedPeer(uid, primary, own, group uint32, groups func(uint32) ([]string, error)) bool {
	if uid == own || primary == group {
		return true
	}
	ids, err := groups(uid)
	if err != nil {
		return false
	}
	for _, id := range ids {
		if id == strconv.FormatUint(uint64(group), 10) {
			return true
		}
	}
	return false
}

func peerAllowed(c *net.UnixConn, group uint32) bool {
	raw, err := c.SyscallConn()
	if err != nil {
		return false
	}
	var cred *unix.Ucred
	var sockErr error
	if err = raw.Control(func(fd uintptr) { cred, sockErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); err != nil || sockErr != nil || cred == nil {
		return false
	}
	return allowedPeer(cred.Uid, cred.Gid, uint32(os.Getuid()), group, func(uid uint32) ([]string, error) {
		u, err := user.LookupId(strconv.FormatUint(uint64(uid), 10))
		if err != nil {
			return nil, err
		}
		return u.GroupIds()
	})
}

type peerListener struct {
	*net.UnixListener
	group uint32
}

func (l peerListener) Accept() (net.Conn, error) {
	for {
		c, err := l.AcceptUnix()
		if err != nil {
			return nil, err
		}
		if peerAllowed(c, l.group) {
			return c, nil
		}
		c.Close()
	}
}
