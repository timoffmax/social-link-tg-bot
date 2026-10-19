package dashboard

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
)

// Listener networks.
const (
	networkTCP  = "tcp"
	networkUnix = "unix"
)

// socketMode lets only the dashboard's user and group open the socket; the
// proxy gets in by being a member of that group.
const socketMode fs.FileMode = 0o660

// ErrSocketPathTaken is returned when the socket path holds a file that is
// not a socket.
var ErrSocketPathTaken = errors.New("socket path exists and is not a socket")

// Listen opens the configured listener: the unix socket (a stale socket from
// an earlier run is replaced, mode 0660, removed again when the listener
// closes) or the TCP address.
func Listen(config Config) (net.Listener, error) {
	var result net.Listener
	var err error

	if config.SocketPath != "" {
		result, err = listenSocket(config.SocketPath)
	}

	if config.SocketPath == "" {
		result, err = net.Listen(networkTCP, config.Addr)
	}

	return result, err
}

// listenSocket listens on path, replacing only a socket left behind.
func listenSocket(path string) (net.Listener, error) {
	var result net.Listener
	err := removeStaleSocket(path)

	if err == nil {
		result, err = net.Listen(networkUnix, path)
	}

	if err == nil {
		err = os.Chmod(path, socketMode)
	}

	if (err != nil) && (result != nil) {
		_ = result.Close()
		result = nil
	}

	return result, err
}

// removeStaleSocket removes a socket file at path; any other file is an
// error, and a missing one is fine.
func removeStaleSocket(path string) error {
	var result error
	info, statErr := os.Lstat(path)
	isMissing := errors.Is(statErr, fs.ErrNotExist)

	switch {
	case isMissing == true:
		result = nil
	case statErr != nil:
		result = statErr
	case (info.Mode() & fs.ModeSocket) == 0:
		result = fmt.Errorf("%w: %s", ErrSocketPathTaken, path)
	default:
		result = os.Remove(path)
	}

	return result
}
