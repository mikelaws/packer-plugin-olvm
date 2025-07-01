package olvm

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"

	"github.com/hashicorp/packer-plugin-sdk/template/interpolate"
)

// AccessConfig contains the OLVM API access and authentication configuration
type AccessConfig struct {
	OlvmURLRaw    string `mapstructure:"olvm_url"`
	olvmParsedURL *url.URL
	TLSInsecure   bool   `mapstructure:"tls_insecure"`
	Username      string `mapstructure:"username"`
	Password      string `mapstructure:"password"`
}

// Prepare performs basic validation on the AccessConfig
func (c *AccessConfig) Prepare(ctx *interpolate.Context) []error {
	var errs []error

	if c.OlvmURLRaw == "" {
		c.OlvmURLRaw = os.Getenv("OLVM_URL")
	}
	if c.Username == "" {
		c.Username = os.Getenv("OLVM_USERNAME")
	}
	if c.Password == "" {
		c.Password = os.Getenv("OLVM_PASSWORD")
	}

	// Required configurations that will display errors if not set
	if c.Username == "" {
		errs = append(errs, errors.New("username must be specified"))
	}
	if c.Password == "" {
		errs = append(errs, errors.New("password must be specified"))
	}
	if c.OlvmURLRaw == "" {
		errs = append(errs, errors.New("olvm_url must be specified"))
	}

	var err error
	if c.olvmParsedURL, err = url.Parse(c.OlvmURLRaw); err != nil {
		errs = append(errs, fmt.Errorf("Could not parse olvm_url: %s", err))
	}

	if len(errs) > 0 {
		return errs
	}

	return nil
}

// getBasicAuth returns the base64-encoded Basic Auth credentials
func (c *AccessConfig) getBasicAuth() string {
	credentials := fmt.Sprintf("%s:%s", c.Username, c.Password)
	return base64.StdEncoding.EncodeToString([]byte(credentials))
}
