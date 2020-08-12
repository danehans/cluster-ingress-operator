package k8s

import (
	"testing"
)

// ValidateURI validates uri as being a valid http(s) uri and returns the uri scheme.
func TestURI(t *testing.T) {
	testCases := []struct {
		description   string
		uri           string
		httpsRequired bool
		expected      bool
	}{
		{
			description:   "valid http uri with IP host and no port",
			uri:           "http://1.2.3.4",
			httpsRequired: false,
			expected:      true,
		},
		{
			description:   "valid http uri with IP host and backslash with no port",
			uri:           "http://1.2.3.4/",
			httpsRequired: false,
			expected:      true,
		},
		{
			description:   "valid http uri with IP host and port",
			uri:           "http://1.2.3.4:80",
			httpsRequired: false,
			expected:      true,
		},
		{
			description:   "valid http uri with IP host, port and backslash",
			uri:           "http://1.2.3.4:80/",
			httpsRequired: false,
			expected:      true,
		},
		{
			description:   "valid http uri with hostname",
			uri:           "http://redhat",
			httpsRequired: false,
			expected:      true,
		},
		{
			description:   "valid http uri with underscore in hostname",
			uri:           "http://red_hat.com",
			httpsRequired: false,
			expected:      true,
		},
		{
			description:   "valid http uri with FQDN",
			uri:           "http://www.redhat.com",
			httpsRequired: false,
			expected:      true,
		},
		{
			description:   "valid http uri with capitalized FQDN",
			uri:           "http://WWW.REDHAT.COM",
			httpsRequired: false,
			expected:      true,
		},
		{
			description:   "valid https uri with IP host and no port",
			uri:           "https://1.2.3.4",
			httpsRequired: true,
			expected:      true,
		},
		{
			description:   "valid https uri with mixed capitalization, port and bckslash",
			uri:           "https://EXAMPLe.com:8080/",
			httpsRequired: true,
			expected:      true,
		},
		{
			description:   "http uri with invalid port number",
			uri:           "http://1.2.3.4:8080808080",
			httpsRequired: true,
			expected:      false,
		},
		{
			description:   "http uri with port number higher that the accepted range",
			uri:           "http://5.6.7.8:65536",
			httpsRequired: false,
			expected:      false,
		},
		{
			description:   "http uri with port number lower that the accepted range",
			uri:           "http://5.6.7.8:0",
			httpsRequired: false,
			expected:      false,
		},
		{
			description:   "missing uri scheme",
			uri:           "redhat.com",
			httpsRequired: false,
			expected:      false,
		},
	}

	for _, tc := range testCases {
		err := ValidateURI(tc.uri, tc.httpsRequired)
		switch {
		case err != nil && tc.expected:
			t.Errorf("test %s failed: %v", tc.description, err)
		case err == nil && !tc.expected:
			t.Errorf("test %s expected to fail, but passed", tc.description)
		}
	}
}
