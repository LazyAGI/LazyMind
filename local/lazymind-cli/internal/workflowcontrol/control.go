// Package workflowcontrol validates legacy presentation URLs without host authentication.
package workflowcontrol

import (
	"errors"
	"net"
	"net/url"
)

func ValidateEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("configure the DSH root URL without credentials")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback()))) {
		return errors.New("DSH requires HTTPS or a loopback HTTP address")
	}
	return nil
}
