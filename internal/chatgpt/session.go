package chatgpt

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// sessionCache persists the bootstrapped session (cookies + octane metadata)
// between invocations so the heavy home page fetch only happens once per
// affinity expiry window instead of once per ask.
type sessionCache struct {
	Cookie        string `json:"cookie"`
	WorkerVersion string `json:"worker_version"`
	BuildID       string `json:"build_id"`
	Affinity      string `json:"affinity"`
	OaiSessionID  string `json:"oai_session_id"`
	DeviceID      string `json:"device_id"`
	SavedAt       int64  `json:"saved_at"`
	ExpiresAt     int64  `json:"expires_at"`
}

func cachePath() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "parley-mcp", "chatgpt-session.json"), nil
}

func (c *client) loadSessionCache() bool {
	path, err := cachePath()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var sc sessionCache
	if err := json.Unmarshal(data, &sc); err != nil {
		return false
	}
	if sc.Affinity == "" || sc.OaiSessionID == "" || sc.DeviceID == "" {
		return false
	}
	if time.Now().Unix() >= sc.ExpiresAt {
		return false
	}
	seedJar(c.http.Jar, sc.Cookie)
	c.home = &homeState{
		workerVersion: sc.WorkerVersion,
		buildID:       sc.BuildID,
		affinity:      sc.Affinity,
		oaiSessionID:  sc.OaiSessionID,
	}
	c.deviceID = sc.DeviceID
	return true
}

func (c *client) saveSessionCache() {
	if c.home == nil || c.deviceID == "" {
		return
	}
	path, err := cachePath()
	if err != nil {
		return
	}
	exp := c.home.affinityExp
	if exp == 0 {
		exp = time.Now().Add(12 * time.Hour).Unix()
	}
	sc := sessionCache{
		Cookie:        jarCookies(c.http.Jar),
		WorkerVersion: c.home.workerVersion,
		BuildID:       c.home.buildID,
		Affinity:      c.home.affinity,
		OaiSessionID:  c.home.oaiSessionID,
		DeviceID:      c.deviceID,
		SavedAt:       time.Now().Unix(),
		ExpiresAt:     exp,
	}
	data, err := json.Marshal(sc)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

// jarCookies serializes the session cookies for chatgpt.com from the jar.
func jarCookies(jar http.CookieJar) string {
	u, _ := url.Parse(chatgptHome)
	var parts []string
	for _, ck := range jar.Cookies(u) {
		parts = append(parts, ck.Name+"="+ck.Value)
	}
	return strings.Join(parts, "; ")
}

// seedJar loads a "name=value; name2=value2" cookie string into the jar.
func seedJar(jar http.CookieJar, cookieStr string) {
	if jar == nil || cookieStr == "" {
		return
	}
	u, _ := url.Parse(chatgptHome)
	var cks []*http.Cookie
	for _, part := range strings.Split(cookieStr, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		cks = append(cks, &http.Cookie{Name: strings.TrimSpace(kv[0]), Value: strings.TrimSpace(kv[1])})
	}
	if len(cks) > 0 {
		jar.SetCookies(u, cks)
	}
}
