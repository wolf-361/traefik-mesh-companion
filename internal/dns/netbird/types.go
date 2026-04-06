package netbird

// Zone represents the structure of a DNS zone in the NetBird API.
type Zone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Record represents a single DNS record within a NetBird zone.
type Record struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
}
