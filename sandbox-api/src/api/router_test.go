package api

import (
	"testing"
)

func TestRedactSecrets(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no query string",
			input:    "/api/endpoint",
			expected: "/api/endpoint",
		},
		{
			name:     "no sensitive params",
			input:    "/api/endpoint?name=test&value=123",
			expected: "/api/endpoint?name=test&value=123",
		},
		{
			name:     "api_key param",
			input:    "/api/endpoint?api_key=secret123&name=test",
			expected: "/api/endpoint?api_key=%5BREDACTED%5D&name=test",
		},
		{
			name:     "apikey param",
			input:    "/api/endpoint?apikey=secret123",
			expected: "/api/endpoint?apikey=%5BREDACTED%5D",
		},
		{
			name:     "api-key param",
			input:    "/api/endpoint?api-key=secret123",
			expected: "/api/endpoint?api-key=%5BREDACTED%5D",
		},
		{
			name:     "token param",
			input:    "/api/endpoint?token=abc123xyz",
			expected: "/api/endpoint?token=%5BREDACTED%5D",
		},
		{
			name:     "access_token param",
			input:    "/api/endpoint?access_token=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
			expected: "/api/endpoint?access_token=%5BREDACTED%5D",
		},
		{
			name:     "password param",
			input:    "/api/endpoint?password=supersecret",
			expected: "/api/endpoint?password=%5BREDACTED%5D",
		},
		{
			name:     "secret param",
			input:    "/api/endpoint?secret=mysecret",
			expected: "/api/endpoint?secret=%5BREDACTED%5D",
		},
		{
			name:     "authorization param",
			input:    "/api/endpoint?authorization=Bearer%20xyz",
			expected: "/api/endpoint?authorization=%5BREDACTED%5D",
		},
		{
			name:     "jwt param",
			input:    "/api/endpoint?jwt=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0",
			expected: "/api/endpoint?jwt=%5BREDACTED%5D",
		},
		{
			name:     "multiple sensitive params",
			input:    "/api/endpoint?api_key=key123&token=token456&name=test",
			expected: "/api/endpoint?api_key=%5BREDACTED%5D&name=test&token=%5BREDACTED%5D",
		},
		{
			name:     "case insensitive API_KEY",
			input:    "/api/endpoint?API_KEY=secret123",
			expected: "/api/endpoint?API_KEY=%5BREDACTED%5D",
		},
		{
			name:     "case insensitive Token",
			input:    "/api/endpoint?Token=abc123",
			expected: "/api/endpoint?Token=%5BREDACTED%5D",
		},
		{
			name:     "case insensitive PASSWORD",
			input:    "/api/endpoint?PASSWORD=secret",
			expected: "/api/endpoint?PASSWORD=%5BREDACTED%5D",
		},
		{
			name:     "session_id param",
			input:    "/api/endpoint?session_id=sess_abc123",
			expected: "/api/endpoint?session_id=%5BREDACTED%5D",
		},
		{
			name:     "client_secret param",
			input:    "/api/endpoint?client_secret=cs_live_xyz",
			expected: "/api/endpoint?client_secret=%5BREDACTED%5D",
		},
		{
			name:     "key param",
			input:    "/api/endpoint?key=mykey123",
			expected: "/api/endpoint?key=%5BREDACTED%5D",
		},
		{
			name:     "credential param",
			input:    "/api/endpoint?credential=cred_value",
			expected: "/api/endpoint?credential=%5BREDACTED%5D",
		},
		{
			name:     "empty query string",
			input:    "/api/endpoint?",
			expected: "/api/endpoint?",
		},
		{
			name:     "empty value for sensitive param still redacted",
			input:    "/api/endpoint?api_key=&name=test",
			expected: "/api/endpoint?api_key=%5BREDACTED%5D&name=test",
		},
		{
			name:     "complex path with sensitive param",
			input:    "/api/v1/users/123/data?api_key=secret&format=json",
			expected: "/api/v1/users/123/data?api_key=%5BREDACTED%5D&format=json",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := redactSecrets(tc.input)
			if result != tc.expected {
				t.Errorf("redactSecrets(%q) = %q, expected %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestRedactQueryPatterns(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "api_key pattern",
			input:    "/api/endpoint?api_key=secret123&name=test",
			expected: "/api/endpoint?api_key=[REDACTED]&name=test",
		},
		{
			name:     "token pattern",
			input:    "/api/endpoint?token=abc123",
			expected: "/api/endpoint?token=[REDACTED]",
		},
		{
			name:     "password pattern",
			input:    "/api/endpoint?password=mysecret",
			expected: "/api/endpoint?password=[REDACTED]",
		},
		{
			name:     "multiple patterns",
			input:    "/api/endpoint?api_key=key1&password=pass1&name=test",
			expected: "/api/endpoint?api_key=[REDACTED]&password=[REDACTED]&name=test",
		},
		{
			name:     "case insensitive API_KEY",
			input:    "/api/endpoint?API_KEY=secret",
			expected: "/api/endpoint?API_KEY=[REDACTED]",
		},
		{
			name:     "no sensitive params",
			input:    "/api/endpoint?name=test&value=123",
			expected: "/api/endpoint?name=test&value=123",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := redactQueryPatterns(tc.input)
			if result != tc.expected {
				t.Errorf("redactQueryPatterns(%q) = %q, expected %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestRedactSecretsPreservesNonSensitiveParams(t *testing.T) {
	// Test that non-sensitive parameters are not modified
	input := "/api/endpoint?user=john&email=john@example.com&id=12345"
	result := redactSecrets(input)

	if result != input {
		t.Errorf("Non-sensitive params should not be modified. Got %q, expected %q", result, input)
	}
}

func TestRedactSecretsHandlesSpecialCharacters(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "url encoded value",
			input:    "/api/endpoint?api_key=secret%20with%20spaces",
			expected: "/api/endpoint?api_key=%5BREDACTED%5D",
		},
		{
			name:     "special characters in non-sensitive param",
			input:    "/api/endpoint?name=test%20name&api_key=secret",
			expected: "/api/endpoint?api_key=%5BREDACTED%5D&name=test+name",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := redactSecrets(tc.input)
			if result != tc.expected {
				t.Errorf("redactSecrets(%q) = %q, expected %q", tc.input, result, tc.expected)
			}
		})
	}
}
