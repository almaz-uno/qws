package main

import (
	"reflect"
	"testing"
	"time"

	"github.com/almaz-uno/qws/internal/config"
)

// TestAnimationFlags checks K2 of specs/010-animation-options: the keys are
// read from their flags, an effect list as names joined by commas
func TestAnimationFlags(t *testing.T) {
	flags := rootCmd.PersistentFlags()
	for name, value := range map[string]string{
		"appearance-animation-enabled":  "false",
		"appearance-animation-duration": "250ms",
		"appearance-animation-step":     "false",
		"appearance-animation-show":     "fade,zoom",
		"appearance-animation-hide":     "none",
		"appearance-animation-hover":    "zoom",
	} {
		if err := flags.Set(name, value); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	cfg = config.Default()
	applyFlags()

	want := config.Animation{
		Enabled: false, Duration: 250 * time.Millisecond, Step: false,
		Show: []string{"fade", "zoom"}, Hide: []string{"none"}, Hover: []string{"zoom"},
	}
	got := cfg.Appearance.Animation
	// The keys of specs/014-appearance-keys are checked by TestAppearanceFlags
	got.HoverDuration, got.OverlayZoom, got.HoverZoom = 0, 0, 0
	if !reflect.DeepEqual(got, want) {
		t.Errorf("flags: %+v, want %+v", got, want)
	}
}

// TestAppearanceFlags checks K5 of specs/014-appearance-keys: the keys are
// read from their flags
func TestAppearanceFlags(t *testing.T) {
	flags := rootCmd.PersistentFlags()
	for name, value := range map[string]string{
		"appearance-header-enabled":           "false",
		"appearance-animation-hover-duration": "60ms",
		"appearance-animation-overlay-zoom":   "0.7",
		"appearance-animation-hover-zoom":     "1.3",
	} {
		if err := flags.Set(name, value); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	cfg = config.Default()
	applyFlags()

	a := cfg.Appearance.Animation
	if cfg.Appearance.Header.Enabled || a.HoverDuration != 60*time.Millisecond || a.OverlayZoom != 0.7 || a.HoverZoom != 1.3 {
		t.Errorf("flags: header %+v, hover duration %v, zooms %v, %v; want hidden, 60ms, 0.7, 1.3",
			cfg.Appearance.Header, a.HoverDuration, a.OverlayZoom, a.HoverZoom)
	}
}
