package k8s

import (
	"fmt"
	"net/url"
	"strconv"

	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	SchemeHTTPS = "https"
)

// ValidateURI validates uri as being a http(s) valid url, returning an error
// if uri is not https when requireHTTPS or uri is invalid.
func ValidateURI(uri string, requireHTTPS bool) error {
	parsed, err := url.ParseRequestURI(uri)
	if err != nil {
		return err
	}
	if requireHTTPS && parsed.Scheme != SchemeHTTPS {
		return fmt.Errorf("%s scheme required but not present in uri %s", SchemeHTTPS, uri)
	}
	if port := parsed.Port(); len(port) != 0 {
		intPort, err := strconv.Atoi(port)
		if err != nil {
			return fmt.Errorf("failed converting port to integer for ValidateURI %q: %v", uri, err)
		}
		if err := ValidatePort(intPort); err != nil {
			return fmt.Errorf("failed to validate port for URL %q: %v", uri, err)
		}
	}

	return nil
}

// ValidatePort validates if port is a valid port number between 1-65535.
func ValidatePort(port int) error {
	invalidPorts := validation.IsValidPortNum(port)
	if invalidPorts != nil {
		return fmt.Errorf("invalid port number: %d", port)
	}

	return nil
}
