package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/lazyagi/lazymind/local_proxy/internal/config"
)

type RouteMatch struct {
	Route      config.RouteConfig
	TargetPath string
	Upstream   string
}

type RouteMatchError interface {
	StatusCode() int
	error
}

type RouteDisabled struct {
	Route config.RouteConfig
}

func (e RouteDisabled) Error() string {
	return fmt.Sprintf("route %q is disabled", e.Route.Name)
}

func (e RouteDisabled) StatusCode() int {
	return http.StatusServiceUnavailable
}

type RouteNotFound struct {
	Path string
}

func (e RouteNotFound) Error() string {
	return fmt.Sprintf("no route matches path %q", e.Path)
}

func (e RouteNotFound) StatusCode() int {
	return http.StatusNotFound
}

func MatchRoute(routes []config.RouteConfig, path string) (RouteMatch, RouteMatchError) {
	var match *config.RouteConfig

	for i := range routes {
		route := &routes[i]
		if !matchesPrefix(path, route.Prefix) {
			continue
		}
		if match == nil || len(route.Prefix) > len(match.Prefix) {
			match = route
		}
	}

	if match == nil {
		return RouteMatch{}, RouteNotFound{Path: path}
	}
	if !match.Enabled {
		return RouteMatch{}, RouteDisabled{Route: *match}
	}

	target := path
	if match.StripPath {
		target = stripPrefix(path, match.Prefix)
	}

	return RouteMatch{
		Route:      *match,
		TargetPath: target,
		Upstream:   match.Upstream,
	}, nil
}

func matchesPrefix(path, prefix string) bool {
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, prefix+"/")
}

func stripPrefix(path, prefix string) string {
	if path == prefix {
		return "/"
	}
	return path[len(prefix):]
}
