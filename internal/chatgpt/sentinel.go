package chatgpt

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"
)

// Sentinel proof-of-work and fingerprint implementation for the chatgpt.com
// anonymous web flow. Ported from the protocol used by the sentinel SDK:
//
//   - requirements token: "gAAAAAC" + base64(json(fingerprint)) + "~S"
//   - proof token:        "gAAAAAB" + base64(json(fingerprint)) + "~S"
//     where the fingerprint embeds a nonce + elapsed time whose
//     FNV-1a(seed + base64(fingerprint)) matches the server difficulty.
//   - turnstile is deliberately never sent.

const (
	prefixRequirements = "gAAAAAC"
	prefixProof        = "gAAAAAB"
	tokenSuffix        = "~S"
)

var (
	scriptURLPool = []string{
		"https://chatgpt.com/unauth-mweb/scripts/declarative-partial-updates-5afb73940f1a.js",
		"https://chatgpt.com/unauth-mweb/assets/octane-home-client-Bw7qql0E.js",
		"https://chatgpt.com/sentinel/20260810913b/sdk.js",
		"https://chatgpt.com/unauth-mweb/assets/sentinel-CefYVExk.js",
		"https://chatgpt.com/unauth-mweb/assets/native-initial-sentinel-prewarm-ifNRyHFK.js",
	}
	navigatorProbes = []string{
		"locks\x00[object LockManager]",
		"mediaSession\x00[object MediaSession]",
		"serial\x00[object Serial]",
		"geolocation\x00[object Geolocation]",
		"serviceWorker\x00[object ServiceWorkerContainer]",
	}
	documentKeys = []string{
		"_homepageMaxxingReport", "location", "onpaste", "onreadystatechange",
		"_reactListening", "hidden", "onfullscreenchange",
	}
	windowKeys = []string{
		"toolbar", "requestIdleCallback", "webkitRequestAnimationFrame",
		"onfocus", "onblur", "chrome",
	}
	fullTzNames = map[string]string{
		"PDT": "Pacific Daylight Time", "PST": "Pacific Standard Time",
		"EDT": "Eastern Daylight Time", "EST": "Eastern Standard Time",
		"CDT": "Central Daylight Time", "CST": "Central Standard Time",
		"MDT": "Mountain Daylight Time", "MST": "Mountain Standard Time",
		"BST": "British Summer Time", "GMT": "Greenwich Mean Time",
		"UTC": "Coordinated Universal Time", "JST": "Japan Standard Time",
		"KST": "Korea Standard Time", "AEST": "Australian Eastern Standard Time",
		"AEDT": "Australian Eastern Daylight Time", "NZST": "New Zealand Standard Time",
		"NZDT": "New Zealand Daylight Time", "CET": "Central European Time",
		"CEST": "Central European Summer Time",
	}
)

// fingerprintConfig builds the 25-element sentinel fingerprint array.
// For the requirements token: nonce=1, elapsed=Math.random().
// For the proof token: nonce=attempt counter, elapsed=ms since PoW start.
func fingerprintConfig(deviceID, ua, buildID string, nonce int, elapsed any) []any {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	nowMs := float64(time.Now().UnixMilli())
	perfNow := float64(int64(rng.Float64()*49000)+1000) + rng.Float64()
	timeOrigin := nowMs - perfNow

	scriptURL := scriptURLPool[rng.Intn(len(scriptURLPool))]
	navigatorProbe := navigatorProbes[rng.Intn(len(navigatorProbes))]
	docKey := documentKeys[rng.Intn(len(documentKeys))]
	winKey := windowKeys[rng.Intn(len(windowKeys))]

	return []any{
		1073 + 882,                 // [0]  screen.width + screen.height
		jsDateToString(time.Now()), // [1]  Date.toString()
		int64(4294967296),          // [2]  performance.memory.jsHeapSizeLimit
		nonce,                      // [3]  nonce / fixed marker
		ua,                         // [4]  navigator.userAgent
		scriptURL,                  // [5]  currentScript.src
		buildID,                    // [6]  documentElement data-build
		"en-US",                    // [7]  navigator.language
		"en-US,en",                 // [8]  navigator.languages.join(",")
		elapsed,                    // [9]  Math.random() or elapsed ms
		navigatorProbe,             // [10] navigator prototype probe
		docKey,                     // [11] document random key
		winKey,                     // [12] window random key
		perfNow,                    // [13] performance.now()
		deviceID,                   // [14] device_id
		"",                         // [15] location.search
		16,                         // [16] navigator.hardwareConcurrency
		timeOrigin,                 // [17] performance.timeOrigin
		0, 0, 0, 0, 0, 0, 0,        // [18-24] window probes
	}
}

// requirementsToken builds the "p" value for sentinel/chat-requirements/prepare.
func requirementsToken(deviceID, ua, buildID string) string {
	config := fingerprintConfig(deviceID, ua, buildID, 1, rand.Float64())
	return prefixRequirements + encodeConfig(config) + tokenSuffix
}

// solveProofOfWork solves the server challenge: find nonce where
// FNV-1a(seed + base64(fingerprint))[:len(difficulty)] <= difficulty.
func solveProofOfWork(seed, difficulty, deviceID, ua, buildID string) string {
	if seed == "" || difficulty == "" {
		return prefixProof + tokenSuffix
	}
	diffLen := len(difficulty)
	if diffLen > 8 {
		diffLen = 8
	}
	start := time.Now()
	const maxIter = 500000
	for i := 0; i < maxIter; i++ {
		elapsed := time.Since(start).Milliseconds()
		config := fingerprintConfig(deviceID, ua, buildID, i, elapsed)
		encoded := encodeConfig(config)
		hash := fnv1aHash(seed + encoded)
		if hash[:diffLen] <= difficulty {
			return prefixProof + encoded + tokenSuffix
		}
	}
	return prefixProof + tokenSuffix
}

func encodeConfig(config []any) string {
	b, err := json.Marshal(config)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(b)
}

// fnv1aHash replicates the JS FNV-1a 32-bit hash with the avalanche finalizer.
func fnv1aHash(text string) string {
	const (
		fnvOffset = 2166136261
		fnvPrime  = 16777619
	)
	h := uint32(fnvOffset)
	for _, ch := range text {
		h ^= uint32(ch)
		h = imul32(h, fnvPrime)
	}
	h ^= h >> 16
	h = imul32(h, 2246822507)
	h ^= h >> 13
	h = imul32(h, 3266489909)
	h ^= h >> 16
	return fmt.Sprintf("%08x", h)
}

func imul32(a, b uint32) uint32 {
	return (a * b) & 0xFFFFFFFF
}

// jsDateToString replicates new Date().toString() in a browser.
func jsDateToString(t time.Time) string {
	head := t.Format("Mon Jan 02 2006 15:04:05")
	_, offset := t.Zone()
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	gmt := fmt.Sprintf("GMT%s%02d%02d", sign, offset/3600, (offset%3600)/60)
	short, _ := t.Zone()
	full := fullTzNames[short]
	if full == "" {
		full = short
	}
	return head + " " + gmt + " (" + full + ")"
}
