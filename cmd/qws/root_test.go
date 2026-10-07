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
		Enabled: "false", Duration: 250 * time.Millisecond, Step: false,
		Show: []string{"fade", "zoom"}, Hide: []string{"none"}, Hover: []string{"zoom"},
	}
	got := cfg.Appearance.Animation
	// The keys of specs/014-appearance-keys are checked by TestAppearanceFlags,
	// those of specs/028-grid-locate by TestLocateFlags, of
	// specs/031-animation-auto by TestAnimationEnabledFlag
	got.HoverDuration, got.OverlayZoom, got.HoverZoom = 0, 0, 0
	got.LocateDuration, got.LocateZoom = 0, 0
	got.VNCPorts = nil
	if !reflect.DeepEqual(got, want) {
		t.Errorf("flags: %+v, want %+v", got, want)
	}
}

// TestThumbnailFlags checks K10 of specs/020-live-thumbnails: the keys of the
// live thumbnails are read from their flags
func TestThumbnailFlags(t *testing.T) {
	flags := rootCmd.PersistentFlags()
	for name, value := range map[string]string{
		"appearance-thumbnail-live":          "false",
		"appearance-thumbnail-live-interval": "15ms",
	} {
		if err := flags.Set(name, value); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	cfg = config.Default()
	applyFlags()

	if got := cfg.Appearance.Thumbnail; got.Live || got.LiveInterval != 15*time.Millisecond {
		t.Errorf("flags: live %v, interval %v; want false, 15ms", got.Live, got.LiveInterval)
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

// TestLocateFlags checks K5 of specs/028-grid-locate: the keys of the
// selection frame converging after a switch to the grid are read from their
// flags
func TestLocateFlags(t *testing.T) {
	flags := rootCmd.PersistentFlags()
	for name, value := range map[string]string{
		"appearance-animation-locate-duration": "650ms",
		"appearance-animation-locate-zoom":     "2.4",
	} {
		if err := flags.Set(name, value); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	cfg = config.Default()
	applyFlags()

	if a := cfg.Appearance.Animation; a.LocateDuration != 650*time.Millisecond || a.LocateZoom != 2.4 {
		t.Errorf("flags: %v, %v; want 650ms, 2.4", a.LocateDuration, a.LocateZoom)
	}
}

// TestLayoutToggleFlag checks K3 of specs/026-layout-keys: the layout key is
// read from its flag, an empty name too
func TestLayoutToggleFlag(t *testing.T) {
	for _, name := range []string{"F3", ""} {
		if err := rootCmd.PersistentFlags().Set("keybindings-layout-toggle", name); err != nil {
			t.Fatal(err)
		}
		cfg = config.Default()
		applyFlags()
		if got := cfg.Keybindings.LayoutToggle; got != name {
			t.Errorf("flag %q: %q", name, got)
		}
	}
}

// TestAnimationEnabledFlag checks K1 of specs/031-animation-auto: the flag of
// appearance.animation.enabled takes auto, true, false, on and off; alone it
// is true, as the bool flag it was; and the ports of the VNC server are read
// from theirs
func TestAnimationEnabledFlag(t *testing.T) {
	flags := rootCmd.PersistentFlags()
	if f := flags.Lookup("appearance-animation-enabled"); f.DefValue != "auto" {
		t.Errorf("default %q, want auto", f.DefValue)
	}
	for _, c := range []struct {
		args []string
		want string
		mode config.AnimationMode
	}{
		{[]string{"--appearance-animation-enabled"}, "true", config.AnimationOn},
		{[]string{"--appearance-animation-enabled=auto"}, "auto", config.AnimationAuto},
		{[]string{"--appearance-animation-enabled=false"}, "false", config.AnimationOff},
		{[]string{"--appearance-animation-enabled=off"}, "off", config.AnimationOff},
		{[]string{"--appearance-animation-enabled=on"}, "on", config.AnimationOn},
		{[]string{"--appearance-animation-enabled=true"}, "true", config.AnimationOn},
	} {
		if err := flags.Parse(c.args); err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		cfg = config.Default()
		applyFlags()
		mode, _ := cfg.Appearance.Animation.Mode()
		if cfg.Appearance.Animation.Enabled != c.want || mode != c.mode {
			t.Errorf("%v: %q, %v; want %q, %v", c.args, cfg.Appearance.Animation.Enabled, mode, c.want, c.mode)
		}
	}

	if err := flags.Parse([]string{"--appearance-animation-vnc-ports=5901,5902"}); err != nil {
		t.Fatal(err)
	}
	cfg = config.Default()
	applyFlags()
	if got := cfg.Appearance.Animation.VNCPorts; !reflect.DeepEqual(got, []int{5901, 5902}) {
		t.Errorf("--appearance-animation-vnc-ports=5901,5902: %v", got)
	}
}
