package server

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

const loopbackAddressError = "SERVER_ADDR must be a loopback address (127.0.0.1, ::1, or localhost) until sign-in exists."

// Config contains the settings used to run the server.
type Config struct {
	ServerAddr  string
	PublicURL   string
	DatabaseURL string
	APIDocs     bool
}

// LoadConfig loads .env from the working directory and validates server settings.
func LoadConfig() (Config, error) {
	if err := loadEnvironment(); err != nil {
		return Config{}, err
	}
	apiDocs, err := apiDocsFromEnvironment(os.Getenv("TERVI_API_DOCS"))
	if err != nil {
		return Config{}, err
	}

	addr := os.Getenv("SERVER_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil || (host != "127.0.0.1" && host != "::1" && host != "localhost") {
		return Config{}, fmt.Errorf("%s", loopbackAddressError)
	}

	publicURL := os.Getenv("TERVI_PUBLIC_URL")
	if publicURL == "" {
		publicURL = "http://localhost:8080"
	}
	parsedURL, err := url.Parse(publicURL)
	if err != nil || !strings.HasPrefix(publicURL, "http://") && !strings.HasPrefix(publicURL, "https://") || parsedURL.Host == "" {
		return Config{}, fmt.Errorf("TERVI_PUBLIC_URL must start with http:// or https:// and include a host")
	}
	publicURL = strings.TrimRight(publicURL, "/")

	databaseURL, err := databaseURLFromEnvironment()
	if err != nil {
		return Config{}, err
	}
	return Config{
		ServerAddr:  addr,
		PublicURL:   publicURL,
		DatabaseURL: databaseURL,
		APIDocs:     apiDocs,
	}, nil
}

func apiDocsFromEnvironment(value string) (bool, error) {
	switch value {
	case "", "off":
		return false, nil
	case "on":
		return true, nil
	default:
		return false, errors.New(`TERVI_API_DOCS must be "on" or "off"`)
	}
}

// LoadDatabaseURL loads .env from the working directory and returns DATABASE_URL.
func LoadDatabaseURL() (string, error) {
	if err := loadEnvironment(); err != nil {
		return "", err
	}
	return databaseURLFromEnvironment()
}

func databaseURLFromEnvironment() (string, error) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return "", fmt.Errorf("DATABASE_URL is required")
	}
	return databaseURL, nil
}

func loadEnvironment() error {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("load .env: %w", err)
	}
	return nil
}
