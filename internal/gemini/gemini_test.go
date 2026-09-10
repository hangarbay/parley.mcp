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
