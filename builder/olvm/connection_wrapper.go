package olvm

import (
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/packer-plugin-sdk/packer"
	ovirtsdk4 "github.com/ovirt/go-ovirt"
)

// ConnectionWrapper wraps the oVirt connection and provides automatic reconnection
// when authentication failures occur due to session timeouts
type ConnectionWrapper struct {
	config     *Config
	connection *ovirtsdk4.Connection
	ui         packer.Ui
}

// NewConnectionWrapper creates a new connection wrapper
func NewConnectionWrapper(config *Config, ui packer.Ui) (*ConnectionWrapper, error) {
	conn, err := ovirtsdk4.NewConnectionBuilder().
		URL(config.AccessConfig.olvmParsedURL.String()).
		Username(config.AccessConfig.Username).
		Password(config.AccessConfig.Password).
		Insecure(config.AccessConfig.TLSInsecure).
		Compress(true).
		Timeout(time.Second * 10).
		Build()
	if err != nil {
		return nil, fmt.Errorf("OLVM: Connection failed, reason: %s", err.Error())
	}

	return &ConnectionWrapper{
		config:     config,
		connection: conn,
		ui:         ui,
	}, nil
}

// GetConnection returns the underlying connection, reconnecting if necessary
func (cw *ConnectionWrapper) GetConnection() (*ovirtsdk4.Connection, error) {
	// Test the connection first
	if err := cw.connection.Test(); err != nil {
		// Check if it's a retryable error
		if cw.isRetryableError(err) {
			cw.ui.Say("Connection test failed, reconnecting to OLVM...")
			return cw.reconnect()
		}
		// For other errors, return the original error
		return nil, err
	}
	return cw.connection, nil
}

// reconnect establishes a new connection to OLVM
func (cw *ConnectionWrapper) reconnect() (*ovirtsdk4.Connection, error) {
	// Close the old connection
	if cw.connection != nil {
		cw.connection.Close()
	}

	// Create a new connection
	conn, err := ovirtsdk4.NewConnectionBuilder().
		URL(cw.config.AccessConfig.olvmParsedURL.String()).
		Username(cw.config.AccessConfig.Username).
		Password(cw.config.AccessConfig.Password).
		Insecure(cw.config.AccessConfig.TLSInsecure).
		Compress(true).
		Timeout(time.Second * 10).
		Build()
	if err != nil {
		return nil, fmt.Errorf("OLVM: Reconnection failed, reason: %s", err.Error())
	}

	// Test the new connection
	if err := conn.Test(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("OLVM: Reconnection test failed, reason: %s", err.Error())
	}

	cw.connection = conn
	cw.ui.Say("Successfully reconnected to OLVM")
	return conn, nil
}

// Close closes the underlying connection
func (cw *ConnectionWrapper) Close() error {
	if cw.connection != nil {
		return cw.connection.Close()
	}
	return nil
}

// ExecuteWithReconnect executes a function that uses the connection, automatically
// reconnecting if communication failures occur
func (cw *ConnectionWrapper) ExecuteWithReconnect(operation func(*ovirtsdk4.Connection) error) error {
	// Try the operation with the current connection
	conn, err := cw.GetConnection()
	if err != nil {
		return err
	}

	err = operation(conn)
	if err != nil {
		// Check if it's a retryable error
		if cw.isRetryableError(err) {
			cw.ui.Say(fmt.Sprintf("Communication error detected, attempting to reconnect and retry (max %d attempts)...", cw.config.MaxRetries))

			// Retry with reconnection
			for attempt := 1; attempt <= cw.config.MaxRetries; attempt++ {
				cw.ui.Say(fmt.Sprintf("Reconnection attempt %d/%d...", attempt, cw.config.MaxRetries))

				// Reconnect
				conn, err = cw.reconnect()
				if err != nil {
					cw.ui.Say(fmt.Sprintf("Reconnection attempt %d failed: %s", attempt, err))
					if attempt == cw.config.MaxRetries {
						return fmt.Errorf("Failed to reconnect after %d attempts: %s", cw.config.MaxRetries, err)
					}
					// Wait before next attempt
					time.Sleep(time.Duration(cw.config.RetryIntervalSec) * time.Second)
					continue
				}

				// Retry the operation
				err = operation(conn)
				if err != nil {
					// Check if it's still a retryable error
					if cw.isRetryableError(err) {
						cw.ui.Say(fmt.Sprintf("Operation still failed after reconnection attempt %d: %s", attempt, err))
						if attempt == cw.config.MaxRetries {
							return fmt.Errorf("Operation failed after %d reconnection attempts: %s", cw.config.MaxRetries, err)
						}
						// Wait before next attempt
						time.Sleep(time.Duration(cw.config.RetryIntervalSec) * time.Second)
						continue
					} else {
						// Non-retryable error, return immediately
						return fmt.Errorf("Operation failed after reconnection: %s", err)
					}
				} else {
					// Operation succeeded
					cw.ui.Say(fmt.Sprintf("Operation succeeded after reconnection attempt %d", attempt))
					return nil
				}
			}

			// Should not reach here, but just in case
			return fmt.Errorf("Operation failed after %d reconnection attempts", cw.config.MaxRetries)
		} else {
			return err
		}
	}

	return nil
}

// isRetryableError determines if an error should trigger a retry
func (cw *ConnectionWrapper) isRetryableError(err error) bool {
	// Authentication errors (session timeouts)
	if _, ok := err.(*ovirtsdk4.AuthError); ok {
		return true
	}

	// Check for network-related errors
	errStr := err.Error()
	networkErrors := []string{
		"connection refused",
		"connection reset",
		"network is unreachable",
		"no route to host",
		"timeout",
		"deadline exceeded",
		"context deadline exceeded",
		"connection lost",
		"broken pipe",
		"connection reset by peer",
	}

	for _, networkErr := range networkErrors {
		if strings.Contains(strings.ToLower(errStr), networkErr) {
			return true
		}
	}

	// Check for XML parsing errors that indicate HTML responses (session timeouts)
	xmlErrors := []string{
		"tag not matched",
		"expect <fault> but got <html>",
		"expect <fault> but got",
		"unexpected token",
		"xml parsing error",
		"invalid xml",
		"parse error",
		"malformed xml",
		"unexpected element",
		"unexpected end element",
	}

	for _, xmlErr := range xmlErrors {
		if strings.Contains(strings.ToLower(errStr), xmlErr) {
			return true
		}
	}

	// Check for authentication and session-related errors
	authErrors := []string{
		"unauthorized",
		"forbidden",
		"authentication failed",
		"session expired",
		"login required",
		"token expired",
		"invalid token",
		"access denied",
	}

	for _, authErr := range authErrors {
		if strings.Contains(strings.ToLower(errStr), authErr) {
			return true
		}
	}

	// Check for HTTP 5xx server errors (retryable)
	if strings.Contains(errStr, "HTTP 5") {
		return true
	}

	// Check for temporary server errors
	if strings.Contains(errStr, "temporary") || strings.Contains(errStr, "temporarily") {
		return true
	}

	// Check for rate limiting (429) - retryable
	if strings.Contains(errStr, "HTTP 429") {
		return true
	}

	return false
}
