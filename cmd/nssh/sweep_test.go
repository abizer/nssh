package main

import (
	"testing"
	"time"
)

func TestParseEtime(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"00:15", 15 * time.Second},
		{"1:30", 90 * time.Second},
		{"02:15:30", 2*time.Hour + 15*time.Minute + 30*time.Second},
		{"4-12:00:00", 4*24*time.Hour + 12*time.Hour},
		{"7-00:00:00", 7 * 24 * time.Hour},
		{"30-23:59:59", 30*24*time.Hour + 23*time.Hour + 59*time.Minute + 59*time.Second},
	}
	for _, c := range cases {
		got, err := parseEtime(c.in)
		if err != nil {
			t.Errorf("parseEtime(%q) err: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseEtime(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseEtimeRejects(t *testing.T) {
	for _, in := range []string{"", "abc", "1", "1:2:3:4", "a:b", "-:00:00"} {
		if _, err := parseEtime(in); err == nil {
			t.Errorf("parseEtime(%q) = nil err, want error", in)
		}
	}
}
