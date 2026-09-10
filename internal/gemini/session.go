package gemini

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ensureSession bootstraps the Google session cookies once and reuses them for
// the lifetime of the process.
func (c *geminiClient) ensureSession(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cookie != "" {
		return nil
	}

	cookie, err := bootstrapFirefox(ctx)
	if err != nil {
		return fmt.Errorf("firefox bootstrap: %w", err)
	}
	c.cookie = cookie
	return nil
}

// bootstrapFirefox launches headless Firefox against the AI Mode page so the
// profile picks up the cookies the search endpoints expect, then reads them
// out of the profile's cookie store.
func bootstrapFirefox(ctx context.Context) (string, error) {
	firefoxPath := os.Getenv("FIREFOX_PATH")
	if firefoxPath == "" {
		firefoxPath = findFirefox()
	}
	if firefoxPath == "" {
		return "", errors.New("firefox not found; set FIREFOX_PATH")
	}

	tmpDir, err := os.MkdirTemp("", "parley-ff-")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	aiURL := "https://www.google.com/search?q=test&udm=50&hl=en&gl=ca&dpr=2"
	cmd := exec.CommandContext(ctx, firefoxPath,
		"--headless",
		"--profile", tmpDir,
		"--no-remote",
		aiURL,
	)
	// Firefox is chatty on stderr: "running in headless mode" at startup and
	// "Exiting due to channel error" while its headless content processes tear
	// down. Neither is actionable, and forwarding them to os.Stderr pollutes the
	// MCP server's logs, so capture them and surface them only if the launch
	// itself fails.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("launch firefox: %w", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-time.After(15 * time.Second):
		cmd.Process.Signal(os.Interrupt)
		time.Sleep(2 * time.Second)
		cmd.Process.Kill()
	case err := <-done:
		if err != nil {
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				return "", fmt.Errorf("firefox: %w: %s", err, msg)
			}
			return "", fmt.Errorf("firefox: %w", err)
		}
	}

	cookieStr, err := readFirefoxCookies(tmpDir + "/cookies.sqlite")
	if err != nil {
		return "", fmt.Errorf("read cookies: %w", err)
	}
	if cookieStr == "" {
		return "", errors.New("no session cookies obtained from Firefox")
	}

	return cookieStr, nil
}

func findFirefox() string {
	for _, path := range []string{
		"/usr/bin/firefox-esr",
		"/usr/bin/firefox",
		"/Applications/Firefox.app/Contents/MacOS/firefox",
		"/Applications/Firefox Developer Edition.app/Contents/MacOS/firefox",
		"/snap/firefox/current/usr/lib/firefox/firefox",
	} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func readFirefoxCookies(cookieFile string) (string, error) {
	if _, err := os.Stat(cookieFile); os.IsNotExist(err) {
		return "", fmt.Errorf("cookies.sqlite not found at %s", cookieFile)
	}

	db, err := sql.Open("sqlite", cookieFile+"?mode=ro")
	if err != nil {
		return "", fmt.Errorf("open cookies db: %w", err)
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT name, value FROM moz_cookies
		WHERE host LIKE '%.google.com'
		AND name IN ('NID', '__Secure-STRP', 'SEARCH_SAMESITE', 'AEC')
	`)
	if err != nil {
		return "", fmt.Errorf("query cookies: %w", err)
	}
	defer rows.Close()

	var parts []string
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			continue
		}
		parts = append(parts, name+"="+value)
	}

	return strings.Join(parts, "; "), nil
}
