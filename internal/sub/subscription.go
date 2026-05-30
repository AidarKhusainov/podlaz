package sub

import "time"

// Format identifies a supported or planned subscription source format.
type Format string

const (
	FormatUnknown Format = "unknown"
	FormatURIList Format = "uri-list"
	FormatBase64  Format = "base64"
	FormatXrayJSON Format = "xray-json"
	FormatSingBox  Format = "sing-box"
	FormatMihomo   Format = "mihomo"
)

// Source describes a remote or local subscription source.
type Source struct {
	ID        string
	Name      string
	URL       string
	Format    Format
	UpdatedAt time.Time
}
