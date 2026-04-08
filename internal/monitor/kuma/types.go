package kuma

// MonitorPayload represents the exhaustive JSON structure for the Uptime Kuma API.
// This allows the companion to handle complex checks beyond simple HTTP GETs.
type MonitorPayload struct {
	// Core Fields
	Type   string `json:"type"` // http, port, ping, keyword, etc.
	Name   string `json:"name"`
	URL    string `json:"url,omitempty"`
	Method string `json:"method,omitempty"`

	// Networking & Auth
	Hostname   string `json:"hostname,omitempty"`
	Port       int    `json:"port,omitempty"`
	AuthMethod string `json:"authMethod,omitempty"` // "basic", "bearer", "ntlm"
	ProxyID    *int   `json:"proxyId,omitempty"`

	// Timing & Retries
	Interval       int `json:"interval"`
	RetryInterval  int `json:"retryInterval"`
	ResendInterval int `json:"resendInterval,omitempty"`
	MaxRetries     int `json:"maxretries"`

	// Validation
	Keyword            string   `json:"keyword,omitempty"`
	IgnoreTLS          bool     `json:"ignoreTls"`
	UpsideDown         bool     `json:"upsideDown"`
	AcceptedCodes      []string `json:"accepted_statuscodes_json"` // Note: Kuma API often expects this key for status ranges
	ExpiryNotification bool     `json:"expiryNotification"`

	// Metadata & UI
	Description string `json:"description,omitempty"`
	Parent      *int   `json:"parent"` // Used for grouping (Monitor ID of the group)

	// Headers and Body (For advanced API monitoring)
	Headers string `json:"headers,omitempty"` // JSON string of headers
	Body    string `json:"body,omitempty"`
}

// MonitorResponse is used to parse existing monitors from the Kuma API.
// We include URL and Method so the companion can accurately pre-populate the cache.
type MonitorResponse struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	URL    string `json:"url"`
	Method string `json:"method"`
	Parent *int   `json:"parent"`
}
