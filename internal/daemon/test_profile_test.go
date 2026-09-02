package daemon

import "github.com/AidarKhusainov/podlaz/internal/profile"

func testVLESSProfile() profile.Profile {
	return profile.Profile{
		ID:           "test-vless",
		Name:         "test vless",
		Source:       profile.SourceImportedURI,
		Engine:       profile.EngineXray,
		Server:       "example.com",
		Port:         443,
		Protocol:     "vless",
		UserIdentity: "11111111-1111-1111-1111-111111111111",
		Transport:    "tcp",
		Security:     "tls",
		Encryption:   "none",
		ServerName:   "example.com",
	}
}
