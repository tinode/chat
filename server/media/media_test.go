package media

import (
	"testing"
)

func TestMatchCORSOrigin(t *testing.T) {
	cases := []struct {
		allowed  []string
		origin   string
		expected string
	}{
		{
			allowed:  []string{"https://example.com"},
			origin:   "https://example.com",
			expected: "https://example.com",
		},
		{
			allowed:  []string{"https://example2.com", "https://example.com"},
			origin:   "https://example.com",
			expected: "https://example.com",
		},
		{
			allowed:  []string{"*"},
			origin:   "https://example.com",
			expected: "https://example.com",
		},
		{
			allowed:  []string{""},
			origin:   "https://example.com",
			expected: "",
		},
		{
			allowed:  []string{},
			origin:   "https://example.com",
			expected: "",
		},
		{
			allowed:  []string{"https://example.com"},
			origin:   "https://sub.example.com",
			expected: "",
		},
		{
			allowed:  []string{"https://*.example.com"},
			origin:   "https://sub.example.com",
			expected: "https://sub.example.com",
		},
		{
			allowed:  []string{"https://*.example.com"},
			origin:   "https://pre.sub.example.com",
			expected: "",
		},
		{
			allowed:  []string{"https://*.example.com", "https://*.sub.example.com"},
			origin:   "https://pre.sub.example.com",
			expected: "https://pre.sub.example.com",
		},
		{
			allowed:  []string{"https://*.*.example.com"},
			origin:   "https://pre.sub.example.com",
			expected: "https://pre.sub.example.com",
		},
		{
			allowed:  []string{"https://*.sub.example.com"},
			origin:   "https://pre.asd.example.com",
			expected: "",
		},
		{
			allowed:  []string{"https://pre.*.example.com"},
			origin:   "https://pre.sub.example.com",
			expected: "",
		},
		{
			allowed:  []string{"https://*.*.*.example.com"},
			origin:   "https://www.pre.sub.example.com",
			expected: "https://www.pre.sub.example.com",
		},
	}

	for _, tc := range cases {
		result := matchCORSOrigin(tc.allowed, tc.origin)
		if result != tc.expected {
			t.Errorf("Match CORS origin got wrong result. Expected %s, got %s", tc.expected, result)
		}
	}
}
