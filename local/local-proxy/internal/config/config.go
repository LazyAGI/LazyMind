package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen            ListenConfig  `yaml:"listen"`
	AllowNonLocalBind bool          `yaml:"allowNonLocalBind"`
	Auth              AuthConfig    `yaml:"auth"`
	CORS              CORSConfig    `yaml:"cors"`
	Timeouts          TimeoutConfig `yaml:"timeouts"`
	Log               LogConfig     `yaml:"log"`
	Routes            []RouteConfig `yaml:"routes"`
}

type ListenConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type AuthConfig struct {
	Mode              string `yaml:"mode"`
	AuthServiceURL    string `yaml:"authServiceURL"`
	AutoLoginAllowLAN bool   `yaml:"autoLoginAllowLAN"`
}

type CORSConfig struct {
	AllowedOrigins []string `yaml:"allowedOrigins"`
}

type TimeoutConfig struct {
	Connect time.Duration `yaml:"connect"`
	Read    time.Duration `yaml:"read"`
	Write   time.Duration `yaml:"write"`
}

type LogConfig struct {
	Level string `yaml:"level"`
	Path  string `yaml:"path"`
}

type RouteConfig struct {
	Name       string `yaml:"name"`
	Prefix     string `yaml:"prefix"`
	Upstream   string `yaml:"upstream"`
	StripPath  bool   `yaml:"stripPath"`
	Enabled    bool   `yaml:"enabled"`
	Optional   bool   `yaml:"optional"`
	HealthPath string `yaml:"healthPath"`
}

func Load(configPath string) (Config, error) {
	cfg := defaultConfig()

	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		configPath = strings.TrimSpace(os.Getenv("LAZYMIND_LOCAL_PROXY_CONFIG"))
	}
	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return Config{}, fmt.Errorf("read config file: %w", err)
		}
		data = []byte(os.ExpandEnv(string(data)))
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config file: %w", err)
		}
	}

	cfg.applyEnvOverrides()
	cfg.normalize()

	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, nil
}

func defaultConfig() Config {
	return Config{
		Listen: ListenConfig{
			Host: "127.0.0.1",
			Port: 5024,
		},
		AllowNonLocalBind: false,
		Auth: AuthConfig{
			Mode:              "local-rbac",
			AuthServiceURL:    "http://127.0.0.1:8000",
			AutoLoginAllowLAN: false,
		},
		CORS: CORSConfig{
			AllowedOrigins: []string{"http://localhost:5173", "http://127.0.0.1:5173"},
		},
		Timeouts: TimeoutConfig{
			Connect: 10 * time.Second,
			Read:    30 * time.Second,
			Write:   30 * time.Second,
		},
		Log: LogConfig{
			Level: "info",
		},
		Routes: defaultRoutes(),
	}
}

func defaultRoutes() []RouteConfig {
	return []RouteConfig{
		{
			Name:       "authservice-route",
			Prefix:     "/api/authservice",
			Upstream:   "http://127.0.0.1:8000",
			StripPath:  false,
			Enabled:    true,
			Optional:   false,
			HealthPath: "/health",
		},
		{
			Name:       "chat-route",
			Prefix:     "/api/chat",
			Upstream:   "http://127.0.0.1:8046",
			StripPath:  false,
			Enabled:    true,
			Optional:   false,
			HealthPath: "/health",
		},
		{
			Name:       "scan-route",
			Prefix:     "/api/scan",
			Upstream:   "http://127.0.0.1:18080",
			StripPath:  false,
			Enabled:    true,
			Optional:   true,
			HealthPath: "/health",
		},
		{
			Name:       "core-route",
			Prefix:     "/api/core",
			Upstream:   "http://127.0.0.1:8001",
			StripPath:  true,
			Enabled:    true,
			Optional:   false,
			HealthPath: "/health",
		},
		{
			Name:       "evo-route",
			Prefix:     "/api/evo",
			Upstream:   "http://127.0.0.1:8047",
			StripPath:  true,
			Enabled:    true,
			Optional:   true,
			HealthPath: "/health",
		},
	}
}

func (c *Config) applyEnvOverrides() {
	if address := strings.TrimSpace(os.Getenv("LAZYMIND_LOCAL_PROXY_ADDRESS")); address != "" {
		c.Listen.Host = address
	}

	if portText := strings.TrimSpace(os.Getenv("LAZYMIND_LOCAL_PROXY_PORT")); portText != "" {
		port, err := strconv.Atoi(portText)
		if err != nil {
			c.Listen.Port = -1
			return
		}
		c.Listen.Port = port
	}

	if allowLAN := parseBoolEnv(os.Getenv("LAZYMIND_LOCAL_AUTO_LOGIN_ALLOW_LAN")); allowLAN != nil {
		c.Auth.AutoLoginAllowLAN = *allowLAN
	}
}

func parseBoolEnv(value string) *bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return nil
	}
	switch normalized {
	case "1", "true", "yes", "on":
		v := true
		return &v
	case "0", "false", "no", "off":
		v := false
		return &v
	default:
		return nil
	}
}

func (c *Config) normalize() {
	if len(c.CORS.AllowedOrigins) == 0 {
		return
	}
	origins := make([]string, 0, len(c.CORS.AllowedOrigins))
	seen := make(map[string]struct{}, len(c.CORS.AllowedOrigins))
	for _, origin := range c.CORS.AllowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			continue
		}
		if _, ok := seen[origin]; ok {
			continue
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}
	c.CORS.AllowedOrigins = origins
}

func (c Config) ListenAddr() string {
	return net.JoinHostPort(c.Listen.Host, strconv.Itoa(c.Listen.Port))
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Listen.Host) == "" {
		return fmt.Errorf("listen.host is required")
	}
	if c.Listen.Port <= 0 || c.Listen.Port > 65535 {
		return fmt.Errorf("listen.port must be between 1 and 65535")
	}
	ip := net.ParseIP(strings.TrimSpace(c.Listen.Host))
	if ip != nil && !ip.IsLoopback() && !c.AllowNonLocalBind {
		return fmt.Errorf("non-loopback listen.host %q requires allowNonLocalBind true", c.Listen.Host)
	}

	authMode := strings.ToLower(strings.TrimSpace(c.Auth.Mode))
	if authMode == "" {
		return fmt.Errorf("auth.mode is required")
	}
	if authMode != "local-rbac" {
		return fmt.Errorf("unsupported auth.mode %q", c.Auth.Mode)
	}
	if strings.TrimSpace(c.Auth.AuthServiceURL) == "" {
		return fmt.Errorf("auth.authServiceURL is required")
	}

	u, err := url.ParseRequestURI(strings.TrimSpace(c.Auth.AuthServiceURL))
	if err != nil {
		return fmt.Errorf("invalid auth.authServiceURL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid auth.authServiceURL: must include scheme and host")
	}

	for _, route := range c.Routes {
		prefix := strings.TrimSpace(route.Prefix)
		if prefix == "" {
			return fmt.Errorf("routes[].prefix is required")
		}
		if !strings.HasPrefix(prefix, "/") {
			return fmt.Errorf("routes[].prefix must start with '/': %q", prefix)
		}
		if strings.TrimSpace(route.Name) == "" {
			return fmt.Errorf("routes[].name is required")
		}
		if strings.TrimSpace(route.Upstream) == "" {
			return fmt.Errorf("route %q upstream is required", route.Name)
		}
		healthPath := strings.TrimSpace(route.HealthPath)
		if healthPath != "" && !strings.HasPrefix(healthPath, "/") {
			return fmt.Errorf("routes[].healthPath %q must start with '/':", route.HealthPath)
		}

		upstream, err := url.ParseRequestURI(strings.TrimSpace(route.Upstream))
		if err != nil {
			return fmt.Errorf("invalid routes[].upstream %q: %w", route.Name, err)
		}
		if upstream.Scheme == "" || upstream.Host == "" {
			return fmt.Errorf("invalid routes[].upstream %q: must include scheme and host", route.Name)
		}
	}
	c.Auth.Mode = authMode
	return nil
}
