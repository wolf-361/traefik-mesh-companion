package gatus

type GatusEndpoint struct {
	Name       string            `json:"name"`
	Group      string            `json:"group"`
	URL        string            `json:"url"`
	Method     string            `json:"method"`
	Interval   string            `json:"interval"`
	Conditions []string          `json:"conditions"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
	Client     *GatusClient      `json:"client,omitempty"`
	UI         *GatusUI          `json:"ui,omitempty"`
}

type GatusClient struct {
	Insecure bool `json:"insecure"`
}

type GatusUI struct {
	HideHostname bool   `json:"hideHostname,omitempty"`
	HideURL      bool   `json:"hideURL,omitempty"`
	Description  string `json:"description,omitempty"`
}
