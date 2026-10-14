package server_test

import (
	"strings"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/server"
)

func setConfigEnv(t *testing.T, addr, publicURL string) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://localhost/tervi")
	t.Setenv("SERVER_ADDR", addr)
	t.Setenv("TERVI_PUBLIC_URL", publicURL)
	t.Setenv("TERVI_CHECK_INTERVAL_SECONDS", "")
	t.Setenv("TERVI_CONNECTION_TIMEOUT_SECONDS", "")
}

func TestDefaultListenAddress(t *testing.T) {
	setConfigEnv(t, "", "")

	cfg, err := server.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.ServerAddr != "127.0.0.1:8080" {
		t.Fatalf("ServerAddr = %q, want %q", cfg.ServerAddr, "127.0.0.1:8080")
	}
}

func TestListenAddressMustBeLoopback(t *testing.T) {
	const wantError = "SERVER_ADDR must be a loopback address (127.0.0.1, ::1, or localhost) until sign-in exists."
	for _, addr := range []string{":8080", "0.0.0.0:8080", "192.168.1.5:8080"} {
		t.Run("reject_"+strings.ReplaceAll(addr, ":", "_"), func(t *testing.T) {
			setConfigEnv(t, addr, "http://localhost:8080")
			_, err := server.LoadConfig()
			if err == nil || err.Error() != wantError {
				t.Fatalf("LoadConfig() error = %v, want %q", err, wantError)
			}
		})
	}

	for _, addr := range []string{"127.0.0.1:9000", "[::1]:8080", "localhost:8080"} {
		t.Run("accept_"+strings.ReplaceAll(addr, ":", "_"), func(t *testing.T) {
			setConfigEnv(t, addr, "http://localhost:8080")
			cfg, err := server.LoadConfig()
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			if cfg.ServerAddr != addr {
				t.Fatalf("ServerAddr = %q, want %q", cfg.ServerAddr, addr)
			}
		})
	}
}

func TestPublicURL(t *testing.T) {
	for _, publicURL := range []string{"banana", "ftp://x"} {
		t.Run("reject_"+strings.ReplaceAll(publicURL, ":", "_"), func(t *testing.T) {
			setConfigEnv(t, "127.0.0.1:8080", publicURL)
			if _, err := server.LoadConfig(); err == nil {
				t.Fatalf("LoadConfig() accepted PublicURL %q", publicURL)
			}
		})
	}

	setConfigEnv(t, "127.0.0.1:8080", "http://localhost:8080/")
	cfg, err := server.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.PublicURL != "http://localhost:8080" {
		t.Fatalf("PublicURL = %q, want %q", cfg.PublicURL, "http://localhost:8080")
	}
}

func TestConnectionTimingSettings(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		setConfigEnv(t, "127.0.0.1:8080", "http://localhost:8080")
		cfg, err := server.LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if cfg.CheckInterval != 10*time.Second || cfg.ConnectionTimeout != 30*time.Second {
			t.Fatalf("timing = (%s, %s), want (10s, 30s)", cfg.CheckInterval, cfg.ConnectionTimeout)
		}
	})

	t.Run("valid values", func(t *testing.T) {
		setConfigEnv(t, "127.0.0.1:8080", "http://localhost:8080")
		t.Setenv("TERVI_CHECK_INTERVAL_SECONDS", "4")
		t.Setenv("TERVI_CONNECTION_TIMEOUT_SECONDS", "13")
		cfg, err := server.LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if cfg.CheckInterval != 4*time.Second || cfg.ConnectionTimeout != 13*time.Second {
			t.Fatalf("timing = (%s, %s), want (4s, 13s)", cfg.CheckInterval, cfg.ConnectionTimeout)
		}
	})

	t.Run("interval above maximum", func(t *testing.T) {
		setConfigEnv(t, "127.0.0.1:8080", "http://localhost:8080")
		t.Setenv("TERVI_CHECK_INTERVAL_SECONDS", "61")
		_, err := server.LoadConfig()
		const want = "TERVI_CHECK_INTERVAL_SECONDS must be a whole number of seconds, from 1 to 60"
		if err == nil || err.Error() != want {
			t.Fatalf("LoadConfig() error = %v, want %q", err, want)
		}
	})

	t.Run("interval must be whole seconds", func(t *testing.T) {
		setConfigEnv(t, "127.0.0.1:8080", "http://localhost:8080")
		t.Setenv("TERVI_CHECK_INTERVAL_SECONDS", "1.5")
		_, err := server.LoadConfig()
		const want = "TERVI_CHECK_INTERVAL_SECONDS must be a whole number of seconds, from 1 to 60"
		if err == nil || err.Error() != want {
			t.Fatalf("LoadConfig() error = %v, want %q", err, want)
		}
	})

	t.Run("timeout must exceed interval", func(t *testing.T) {
		setConfigEnv(t, "127.0.0.1:8080", "http://localhost:8080")
		t.Setenv("TERVI_CHECK_INTERVAL_SECONDS", "5")
		t.Setenv("TERVI_CONNECTION_TIMEOUT_SECONDS", "5")
		_, err := server.LoadConfig()
		const want = "TERVI_CONNECTION_TIMEOUT_SECONDS must be a whole number of seconds, greater than TERVI_CHECK_INTERVAL_SECONDS"
		if err == nil || err.Error() != want {
			t.Fatalf("LoadConfig() error = %v, want %q", err, want)
		}
	})
}
