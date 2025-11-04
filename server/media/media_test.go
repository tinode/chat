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
			origin:   "",
			expected: "",
		},
		{
			allowed:  []string{"https://example.com"},
			origin:   "",
			expected: "",
		},
		{
			allowed:  []string{},
			origin:   "https://example.com",
			expected: "",
		},
		{
			allowed:  []string{"http://example.com"},
			origin:   "https://example.com",
			expected: "",
		},
		{
			allowed:  []string{"https://example.com"},
			origin:   "http://example.com",
			expected: "",
		},
		{
			allowed:  []string{"http://example.com:8000"},
			origin:   "http://example.com:8000",
			expected: "http://example.com:8000",
		},
		{
			allowed:  []string{"http://localhost:8090"},
			origin:   "http://localhost:8090",
			expected: "http://localhost:8090",
		},
		{
			allowed:  []string{"http://localhost:8090"},
			origin:   "http://localhost:8080",
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
			expected: "https://pre.sub.example.com",
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
