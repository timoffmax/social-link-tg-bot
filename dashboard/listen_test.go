package dashboard

import (
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigUnixSocket(t *testing.T) {
	config, err := LoadConfig(envMap(map[string]string{EnvAddr: "unix:/run/social-link-dashboard/dashboard.sock", EnvAllowedHosts: "stats.example.com:8443"}))

	if (err != nil) || (config.SocketPath != "/run/social-link-dashboard/dashboard.sock") || (len(config.AllowedHosts) != 1) || (config.AllowedHosts[0] != "stats.example.com:8443") {
		t.Errorf("config = %+v, err = %v", config, err)
	}
}

func TestLoadConfigUnixSocketNeedsAllowedHosts(t *testing.T) {
	_, err := LoadConfig(envMap(map[string]string{EnvAddr: "unix:/run/dashboard.sock", EnvAllowedHosts: " , "}))

	if errors.Is(err, ErrSocketHosts) == false {
		t.Errorf("err = %v", err)
	}
}

func TestLoadConfigUnixSocketNeedsAnAbsolutePath(t *testing.T) {
	for _, addr := range []string{"unix:dashboard.sock", "unix:"} {
		_, err := LoadConfig(envMap(map[string]string{EnvAddr: addr, EnvAllowedHosts: "stats.example.com:8443"}))

		if errors.Is(err, ErrSocketPath) == false {
			t.Errorf("%s: err = %v", addr, err)
		}
	}
}

func TestLoadConfigTCPAddressHasNoSocket(t *testing.T) {
	config, err := LoadConfig(envMap(map[string]string{}))

	if (err != nil) || (config.SocketPath != "") {
		t.Errorf("config = %+v, err = %v", config, err)
	}
}

func TestListenCreatesTheSocketOwnerAndGroupOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dashboard.sock")
	listener, err := Listen(Config{Addr: "unix:" + path, SocketPath: path})

	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	info, statErr := os.Stat(path)

	if (statErr != nil) || ((info.Mode() & fs.ModeSocket) == 0) || (info.Mode().Perm() != 0o660) {
		t.Errorf("info = %v, err = %v", info, statErr)
	}

	connection, dialErr := net.Dial("unix", path)

	if dialErr != nil {
		t.Errorf("dial: %v", dialErr)
	}

	if connection != nil {
		connection.Close()
	}

	listener.Close()

	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) == false {
		t.Errorf("socket left after close: %v", err)
	}
}

func TestListenReplacesAStaleSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dashboard.sock")
	stale, err := net.Listen("unix", path)

	if err != nil {
		t.Fatalf("stale: %v", err)
	}

	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	stale.Close()
	listener, err := Listen(Config{SocketPath: path})

	if err != nil {
		t.Fatalf("listen over a stale socket: %v", err)
	}

	listener.Close()
}

func TestListenRefusesToReplaceAnotherFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dashboard.sock")

	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := Listen(Config{SocketPath: path})
	content, readErr := os.ReadFile(path)

	if (errors.Is(err, ErrSocketPathTaken) == false) || (readErr != nil) || (string(content) != "keep") {
		t.Errorf("err = %v, content = %q, read = %v", err, content, readErr)
	}
}

func TestListenOnTCPByDefault(t *testing.T) {
	listener, err := Listen(Config{Addr: "127.0.0.1:0"})

	if (err != nil) || (listener.Addr().Network() != "tcp") {
		t.Fatalf("listener = %v, err = %v", listener, err)
	}

	listener.Close()
}
