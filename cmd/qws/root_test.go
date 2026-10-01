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
	if got := cfg.Appearance.Animation; !reflect.DeepEqual(got, want) {
		t.Errorf("flags: %+v, want %+v", got, want)
	}
}
