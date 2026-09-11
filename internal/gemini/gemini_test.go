package gemini

import (
	"strings"
	"testing"
)

func TestCheckRequestURL(t *testing.T) {
	if err := checkRequestURL("https://www.google.com/search?" + strings.Repeat("a", maxRequestURL-40)); err != nil {
		t.Fatalf("under limit: unexpected error %v", err)
	}
	if err := checkRequestURL("https://www.google.com/search?" + strings.Repeat("a", maxRequestURL)); err == nil {
		t.Fatal("at limit: expected an error")
	}
}

func TestSearchStatusError(t *testing.T) {
	if err := searchStatusError(200, "page"); err != nil {
		t.Fatalf("200: unexpected error %v", err)
	}
	err := searchStatusError(429, "unusual traffic")
	if err == nil {
		t.Fatal("429: expected an error")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Fatalf("429 error should name the status, got %q", err)
	}
	if err := searchStatusError(500, "boom"); err == nil {
		t.Fatal("500: expected an error")
	}
}
