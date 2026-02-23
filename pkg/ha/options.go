// Copyright 2024 The argocd-agent Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ha

import (
	"crypto/tls"
	"fmt"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/auth"
)

// Options contains configuration for the HA controller
type Options struct {
	// Enabled indicates whether HA is enabled
	Enabled bool

	// PreferredRole is the preferred role for this principal ("primary" or "replica")
	PreferredRole Role

	// PeerAddress is the address of the peer principal (host:port)
	PeerAddress string

	// FailoverTimeout is how long to wait before promoting to standby after losing primary
	FailoverTimeout time.Duration

	// ReplicationPort is the port to use for replication traffic
	ReplicationPort int

	// TLSConfig is the TLS configuration for replication connections
	TLSConfig *tls.Config

	// AdminPort is the port for the localhost-only admin gRPC server (HAAdmin)
	AdminPort int

	// AllowedReplicationClients is the list of identities allowed to connect
	// for replication. Identities are extracted via AuthMethod.
	AllowedReplicationClients []string

	// AuthMethod is the auth method used to authenticate replication connections
	AuthMethod auth.Method

	// ReconnectBackoffInitial is the initial backoff duration for reconnection attempts
	ReconnectBackoffInitial time.Duration

	// ReconnectBackoffMax is the maximum backoff duration for reconnection attempts
	ReconnectBackoffMax time.Duration

	// ReconnectBackoffFactor is the factor to multiply backoff by after each attempt
	ReconnectBackoffFactor float64
}

// DefaultOptions returns the default HA options
func DefaultOptions() *Options {
	return &Options{
		Enabled:                 false,
		PreferredRole:           RoleReplica,
		FailoverTimeout:         30 * time.Second,
		ReplicationPort:         8404,
		AdminPort:               8405,
		ReconnectBackoffInitial: 1 * time.Second,
		ReconnectBackoffMax:     30 * time.Second,
		ReconnectBackoffFactor:  1.5,
	}
}

// Option is a function that modifies Options
type Option func(*Options) error

// WithEnabled sets whether HA is enabled
func WithEnabled(enabled bool) Option {
	return func(o *Options) error {
		o.Enabled = enabled
		return nil
	}
}

// WithPreferredRole sets the preferred role for this principal
func WithPreferredRole(role string) Option {
	return func(o *Options) error {
		r, err := ParseRole(role)
		if err != nil {
			return err
		}
		o.PreferredRole = r
		return nil
	}
}

// WithPeerAddress sets the address of the peer principal
func WithPeerAddress(address string) Option {
	return func(o *Options) error {
		if address == "" {
			return fmt.Errorf("peer address cannot be empty")
		}
		o.PeerAddress = address
		return nil
	}
}

// WithFailoverTimeout sets the failover timeout duration
func WithFailoverTimeout(timeout time.Duration) Option {
	return func(o *Options) error {
		if timeout < 0 {
			return fmt.Errorf("failover timeout cannot be negative")
		}
		o.FailoverTimeout = timeout
		return nil
	}
}

// WithReplicationPort sets the port for replication traffic
func WithReplicationPort(port int) Option {
	return func(o *Options) error {
		if port < 1 || port > 65535 {
			return fmt.Errorf("replication port must be between 1 and 65535")
		}
		o.ReplicationPort = port
		return nil
	}
}

// WithTLSConfig sets the TLS configuration for replication
func WithTLSConfig(config *tls.Config) Option {
	return func(o *Options) error {
		o.TLSConfig = config
		return nil
	}
}

// WithAdminPort sets the port for the localhost-only admin gRPC server
func WithAdminPort(port int) Option {
	return func(o *Options) error {
		if port < 1 || port > 65535 {
			return fmt.Errorf("admin port must be between 1 and 65535")
		}
		o.AdminPort = port
		return nil
	}
}

// WithAllowedReplicationClients sets the list of identities allowed to connect for replication
func WithAllowedReplicationClients(clients []string) Option {
	return func(o *Options) error {
		o.AllowedReplicationClients = clients
		return nil
	}
}

// WithAuthMethod sets the auth method for authenticating replication connections
func WithAuthMethod(method auth.Method) Option {
	return func(o *Options) error {
		o.AuthMethod = method
		return nil
	}
}

// WithReconnectBackoff sets the reconnection backoff parameters
func WithReconnectBackoff(initial, max time.Duration, factor float64) Option {
	return func(o *Options) error {
		if initial < 0 {
			return fmt.Errorf("initial backoff cannot be negative")
		}
		if max < initial {
			return fmt.Errorf("max backoff cannot be less than initial")
		}
		if factor < 1.0 {
			return fmt.Errorf("backoff factor must be at least 1.0")
		}
		o.ReconnectBackoffInitial = initial
		o.ReconnectBackoffMax = max
		o.ReconnectBackoffFactor = factor
		return nil
	}
}

// Validate validates the options
func (o *Options) Validate() error {
	if !o.Enabled {
		return nil
	}

	if o.PeerAddress == "" && o.PreferredRole == RoleReplica {
		return fmt.Errorf("peer address is required for replica role")
	}

	return nil
}
