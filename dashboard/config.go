package dashboard

import (
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"
)

// Environment variables read by the dashboard binary (not the bot's .env).
const (
	EnvAddr         = "DASHBOARD_ADDR"
	EnvDatabaseURL  = "DASHBOARD_DATABASE_URL"
	EnvTimezone     = "DASHBOARD_TIMEZONE"
	EnvAllowPublic  = "DASHBOARD_ALLOW_PUBLIC"
	EnvAllowedHosts = "DASHBOARD_ALLOWED_HOSTS"
)

// Defaults used when an environment variable is empty. The database URL
// uses peer auth over the local unix socket.
const (
	DefaultAddr        = "127.0.0.1:8090"
	DefaultDatabaseURL = "postgres:///social_link_bot?host=/var/run/postgresql&sslmode=disable"
	DefaultTimezone    = "Europe/Kyiv"
)

// allowPublicValue is the only value of EnvAllowPublic that allows a
// non-loopback listen address.
const allowPublicValue = "true"

// localhostName is the host name accepted as loopback without resolving it.
const localhostName = "localhost"

// loopbackIPv4 and loopbackIPv6 are allowed request hosts by default, with
// the port of the listen address.
const (
	loopbackIPv4 = "127.0.0.1"
	loopbackIPv6 = "::1"
)

// Default HTTP and HTTPS ports: browsers leave them out of the Host header.
const (
	defaultHTTPPort  = "80"
	defaultHTTPSPort = "443"
)

// noPort and portSeparator build a Host header without a port.
const (
	noPort        = ""
	portSeparator = ":"
)

// allowedHostSeparator separates the entries of EnvAllowedHosts.
const allowedHostSeparator = ","

// localTimezoneName is accepted by time.LoadLocation but unknown to
// PostgreSQL and to the browser's Intl API.
const localTimezoneName = "Local"

// ErrPublicAddress is returned for a listen address outside loopback.
var ErrPublicAddress = errors.New("listen address is not a loopback address")

// ErrLocalTimezone is returned when the timezone is the "Local" alias.
var ErrLocalTimezone = errors.New("timezone \"Local\" is not an IANA name")

// Config is the dashboard's runtime configuration.
type Config struct {
	Addr        string
	DatabaseURL string
	Timezone    string
	Location    *time.Location
	// AllowedHosts are the lower-cased Host header values (host:port, as
	// the browser sends them) the dashboard answers; any other Host is a
	// DNS-rebinding page and gets 403.
	AllowedHosts []string
}

// LoadConfig reads the configuration through getenv (os.Getenv in main),
// applies the defaults and validates the address and the timezone.
func LoadConfig(getenv func(string) string) (Config, error) {
	result := Config{
		Addr:        envOrDefault(getenv, EnvAddr, DefaultAddr),
		DatabaseURL: envOrDefault(getenv, EnvDatabaseURL, DefaultDatabaseURL),
		Timezone:    envOrDefault(getenv, EnvTimezone, DefaultTimezone),
	}
	allowPublicRaw := getenv(EnvAllowPublic)
	allowPublic := (strings.TrimSpace(allowPublicRaw) == allowPublicValue)
	err := CheckListenAddress(result.Addr, allowPublic)

	if err == nil {
		result.Location, err = loadTimezone(result.Timezone)
	}

	if err == nil {
		result.AllowedHosts = loadAllowedHosts(getenv, result.Addr)
	}

	return result, err
}

// loadAllowedHosts reads the comma-separated EnvAllowedHosts, or defaults
// to the listen address plus localhost, 127.0.0.1 and [::1] on its port.
func loadAllowedHosts(getenv func(string) string, addr string) []string {
	configuredRaw := getenv(EnvAllowedHosts)
	configured := strings.TrimSpace(configuredRaw)
	result := defaultAllowedHosts(addr)

	if configured != "" {
		result = parseAllowedHosts(configured)
	}

	return result
}

// parseAllowedHosts splits, trims and lower-cases the configured hosts.
func parseAllowedHosts(configured string) []string {
	result := []string{}
	entries := strings.Split(configured, allowedHostSeparator)

	for _, entry := range entries {
		host := strings.TrimSpace(entry)

		if host != "" {
			lowerHost := strings.ToLower(host)
			result = append(result, lowerHost)
		}
	}

	return result
}

// defaultAllowedHosts is localhost, 127.0.0.1 and [::1] on the port of the
// listen address, plus the listen host:port unless it is all interfaces. On
// port 80 or 443 each host is also allowed without the port, as browsers
// send it.
func defaultAllowedHosts(addr string) []string {
	splitHost, port, _ := net.SplitHostPort(addr)
	host := strings.ToLower(splitHost)
	names := []string{localhostName, loopbackIPv4, loopbackIPv6}
	ip := net.ParseIP(host)
	isUnspecified := (host == "") || ((ip != nil) && (ip.IsUnspecified() == true))
	isListed := slices.Contains(names, host)
	isDefaultPort := (port == defaultHTTPPort) || (port == defaultHTTPSPort)

	if (isUnspecified == false) && (isListed == false) {
		names = append(names, host)
	}

	result := make([]string, 0, len(names))

	for _, name := range names {
		hostPort := net.JoinHostPort(name, port)
		lowerHostPort := strings.ToLower(hostPort)
		result = append(result, lowerHostPort)
	}

	if isDefaultPort == true {
		result = appendBareHosts(result, names)
	}

	return result
}

// appendBareHosts adds each name as a Host header without a port; an IPv6
// address keeps its brackets.
func appendBareHosts(hosts []string, names []string) []string {
	result := hosts

	for _, name := range names {
		emptyPortHost := net.JoinHostPort(name, noPort)
		bareHost := strings.TrimSuffix(emptyPortHost, portSeparator)
		lowerBareHost := strings.ToLower(bareHost)
		result = append(result, lowerBareHost)
	}

	return result
}

// loadTimezone loads an IANA timezone and refuses the "Local" alias.
func loadTimezone(name string) (*time.Location, error) {
	var result *time.Location
	err := fmt.Errorf("%w: set %s to a name such as %s", ErrLocalTimezone, EnvTimezone, DefaultTimezone)

	if name != localTimezoneName {
		result, err = time.LoadLocation(name)
	}

	return result, err
}

// envOrDefault returns the trimmed variable, or fallback when it is empty.
func envOrDefault(getenv func(string) string, name string, fallback string) string {
	value := getenv(name)
	result := strings.TrimSpace(value)

	if result == "" {
		result = fallback
	}

	return result
}

// CheckListenAddress refuses a host:port whose host is not loopback, unless
// allowPublic is set. An empty host (all interfaces) is not loopback.
func CheckListenAddress(addr string, allowPublic bool) error {
	host, _, err := net.SplitHostPort(addr)
	isRefused := (err == nil) && (allowPublic == false) && (isLoopbackHost(host) == false)

	if isRefused == true {
		err = fmt.Errorf("%w: %q (set %s=%s to allow it)", ErrPublicAddress, addr, EnvAllowPublic, allowPublicValue)
	}

	return err
}

// isLoopbackHost reports whether the host is localhost or a loopback IP.
func isLoopbackHost(host string) bool {
	result := strings.EqualFold(host, localhostName)
	ip := net.ParseIP(host)

	if ip != nil {
		result = ip.IsLoopback()
	}

	return result
}
