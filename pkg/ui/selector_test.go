package ui

import (
	"testing"

	"github.com/almaz-uno/qws/internal/config"
)

// TestSetHeader checks K1 of specs/014-appearance-keys: with
// appearance.header.enabled the header shows the hostname and the version;
// without it, neither — the frame of specs/001-rendering-speed, whose scenes
// have no header
func TestSetHeader(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		appearance := config.Default().Appearance
		appearance.Header.Enabled = enabled
		s := &Selector{appearance: appearance}
		s.SetHeader("ws1", "v1.2.0")

		hostname, version := "ws1", "v1.2.0"
		if !enabled {
			hostname, version = "", ""
		}
		if s.config.Hostname != hostname || s.config.Version != version {
			t.Errorf("enabled %v: hostname %q, version %q; want %q, %q",
				enabled, s.config.Hostname, s.config.Version, hostname, version)
		}
	}
}
