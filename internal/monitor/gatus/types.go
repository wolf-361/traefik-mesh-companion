package gatus

// EndpointPayload represents the JSON structure expected by our custom Gatus Bridge.
type EndpointPayload struct {
	Name       string            `json:"name"`
	Group      string            `json:"group"`
	URL        string            `json:"url"`
	Method     string            `json:"method,omitempty"`
	Interval   string            `json:"interval,omitempty"`
	Conditions []string          `json:"conditions,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
}
