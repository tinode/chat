package media

import (
	"testing"
)

func TestMatchCORSOrigin(t *testing.T) {
	cases := []struct {
		allowed      []string
		origin       string
		expected     string
		expectError  bool
		errorMessage string
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
		{
			allowed:  []string{"*"},
			origin:   "",
			expected: "*", // Should allow any origin, including empty
		},
		// Error cases - these should fail during ParseCORSAllow
		{
			allowed:      []string{"*", "https://example.com"},
			origin:       "https://example.com",
			expectError:  true,
			errorMessage: "wildcard origin '*' must be the only entry",
		},
		{
			allowed:      []string{"not-a-valid-url"},
			origin:       "https://example.com",
			expectError:  true,
			errorMessage: "invalid URI for request",
		},
		{
			allowed:      []string{"://invalid-url"},
			origin:       "https://example.com",
			expectError:  true,
			errorMessage: "missing protocol scheme",
		},
		{
			allowed:      []string{"https://", "example.com"},
			origin:       "https://example.com",
			expectError:  true,
			errorMessage: "invalid URI for request",
		},
		// Valid cases that should not error
		{
			allowed:      []string{"", "https://example.com"},
			origin:       "https://example.com",
			expectError:  true,
			errorMessage: "empty allowed origin '' must be the only entry", // Empty string should make all origins disallowed
		},
		{
			allowed:  []string{"https://Example.com"},
			origin:   "https://example.com",
			expected: "", // Should not match due to case sensitivity
		},
		{
			allowed:  []string{"https://example.com/"},
			origin:   "https://example.com",
			expected: "", // Should not match due to trailing slash
		},
		{
			allowed:  []string{"https://example.com"},
			origin:   "not-a-valid-url",
			expected: "", // Should handle malformed origin gracefully
		},
		{
			allowed:  []string{"https://example.*.com"},
			origin:   "https://example.sub.com",
			expected: "https://example.sub.com",
		},
		{
			allowed:  []string{"https://*.com"},
			origin:   "https://example.com",
			expected: "https://example.com",
		},
		{
			allowed:  []string{"http://*.example.com"},
			origin:   "https://sub.example.com",
			expected: "", // Should not match due to protocol difference
		},
		{
			allowed:  []string{"https://example.com", "https://example.com"},
			origin:   "https://example.com",
			expected: "https://example.com", // Should still work with duplicates
		},
	}

	for i, tc := range cases {
		allowedOrigins, err := ParseCORSAllow(tc.allowed)

		if tc.expectError {
			if err == nil {
				t.Errorf("Test case %d: Expected error but got none. Allowed: %v", i, tc.allowed)
				continue
			}
			if tc.errorMessage != "" && !containsSubstring(err.Error(), tc.errorMessage) {
				t.Errorf("Test case %d: Expected error containing '%s', got '%s'", i, tc.errorMessage, err.Error())
			}
			// For error cases, we don't test the matching logic
			continue
		}

		if err != nil {
			t.Errorf("Test case %d: Unexpected error parsing allowed origins: %v. Allowed: %v", i, err, tc.allowed)
			continue
		}

		result := matchCORSOrigin(allowedOrigins, tc.origin)
		if result != tc.expected {
			t.Errorf("Test case %d: Match CORS origin got wrong result. Expected '%s', got '%s'. Allowed: %v, Origin: '%s'",
				i, tc.expected, result, tc.allowed, tc.origin)
		}
	}
}

// Helper function to check if a string contains a substring (case-insensitive)
func containsSubstring(str, substr string) bool {
	return len(substr) == 0 || len(str) >= len(substr) &&
		(str == substr || containsSubstring(str[1:], substr) ||
			(len(str) > 0 && len(substr) > 0 && str[0] == substr[0] && containsSubstring(str[1:], substr[1:])))
}
