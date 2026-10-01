package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// Criteria of specs/002-config-names

// TestEnvironment checks K1: QWS_* variables are read under the documented
// names, over the file
func TestEnvironment(t *testing.T) {
	clearEnvironment(t)
	file := writeFile(t, "appearance:\n  renderer: cpu\nbehavior:\n  snapshot_interval: 5s\n")
	t.Setenv("QWS_APPEARANCE_RENDERER", "glx")
	t.Setenv("QWS_BEHAVIOR_SNAPSHOT_INTERVAL", "3s")
	t.Setenv("QWS_APPEARANCE_COLORS_DARK_SELECTION_FRAME", "#123456")

	cfg, _, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Appearance.Renderer != "glx" {
		t.Errorf("renderer %q, want glx", cfg.Appearance.Renderer)
	}
	if cfg.Behavior.SnapshotInterval != 3*time.Second {
		t.Errorf("snapshot interval %v, want 3s", cfg.Behavior.SnapshotInterval)
	}
	if cfg.Appearance.Colors.Dark.SelectionFrame != "#123456" {
		t.Errorf("dark selection frame %q, want #123456", cfg.Appearance.Colors.Dark.SelectionFrame)
	}
}

// TestConfigVariable checks K2: QWS_CONFIG names the file, and a file given
// explicitly, as --config does, takes precedence
func TestConfigVariable(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("QWS_CONFIG", writeFile(t, "appearance:\n  spacing: 111\n"))

	cfg, _, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Appearance.Spacing != 111 {
		t.Errorf("spacing %v, want 111 from QWS_CONFIG", cfg.Appearance.Spacing)
	}

	cfg, _, err = Load(writeFile(t, "appearance:\n  spacing: 222\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Appearance.Spacing != 222 {
		t.Errorf("spacing %v, want 222 from the explicit file", cfg.Appearance.Spacing)
	}
}

// TestRoundTrip checks K3: what config init writes is read back as it was
func TestRoundTrip(t *testing.T) {
	clearEnvironment(t)
	want := changedConfig(t)
	data, err := yaml.Marshal(want) // as config init does
	if err != nil {
		t.Fatal(err)
	}

	got, warnings, err := Load(writeFile(t, string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("read back\n%+v\nwritten\n%+v", got, want)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings %v, want none", warnings)
	}
}

// TestTagNames checks K3: every field has the same yaml and mapstructure name
func TestTagNames(t *testing.T) {
	walkFields(reflect.TypeOf(Config{}), "", func(f reflect.StructField, path string) {
		if y, m := f.Tag.Get("yaml"), f.Tag.Get("mapstructure"); y != m {
			t.Errorf("%s: yaml name %q, mapstructure name %q", path, y, m)
		}
	})
}

// TestJoinedKeys checks K4 of specs/002-config-names as specs/015-no-joined-keys
// changes it (K1, K2): a file written with joined names, as config init
// wrote it, has its joined keys not read — the keys they stand for keep their
// defaults — with a warning per joined key naming the key to use, and a key
// with underscores wins over its joined form
func TestJoinedKeys(t *testing.T) {
	clearEnvironment(t)
	written := changedConfig(t)
	data, err := yaml.Marshal(joined(reflect.ValueOf(*written)))
	if err != nil {
		t.Fatal(err)
	}

	got, warnings, err := Load(writeFile(t, string(data)))
	if err != nil {
		t.Fatal(err)
	}
	// Every field differs from its default: a joined one read would show
	want := *written
	unjoined(reflect.ValueOf(&want).Elem(), reflect.ValueOf(*Default()))
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("read\n%+v\nwant\n%+v", *got, want)
	}
	keys := joinedKeys()
	if len(warnings) != len(keys) {
		t.Errorf("%d warnings, want %d: %v", len(warnings), len(keys), warnings)
	}
	for _, w := range warnings {
		named := false
		for k, key := range keys {
			named = named || strings.Contains(w, "key "+k+" ") && strings.HasSuffix(w, "rename it to "+key)
		}
		if !strings.Contains(w, "no longer read") || !named {
			t.Errorf("warning %q does not say the key is not read and which to use", w)
		}
	}

	got, warnings, err = Load(writeFile(t, "behavior:\n  snapshotinterval: 7s\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Behavior.SnapshotInterval != Default().Behavior.SnapshotInterval {
		t.Errorf("snapshot interval %v, want the default %v", got.Behavior.SnapshotInterval, Default().Behavior.SnapshotInterval)
	}
	if len(warnings) != 1 || warnings[0] !=
		"configuration key behavior.snapshotinterval is no longer read: rename it to behavior.snapshot_interval" {
		t.Errorf("warnings %q, want one naming behavior.snapshot_interval", warnings)
	}

	got, warnings, err = Load(writeFile(t, "behavior:\n  snapshot_interval: 2s\n  snapshotinterval: 7s\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Behavior.SnapshotInterval != 2*time.Second {
		t.Errorf("snapshot interval %v, want 2s", got.Behavior.SnapshotInterval)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "ignored") {
		t.Errorf("warnings %v, want one saying the joined key is ignored", warnings)
	}
}

// Criteria of specs/010-animation-options

// TestAnimationDefaults checks K1: the defaults the author chose, and those
// of the keys of specs/014-appearance-keys
func TestAnimationDefaults(t *testing.T) {
	want := Animation{
		Enabled: true, Duration: 150 * time.Millisecond, Step: true,
		Show: []string{"fade", "zoom"}, Hide: []string{"fade", "zoom"}, Hover: []string{"fade", "zoom"},
		OverlayZoom: 0.92, HoverZoom: 1.05,
	}
	if got := Default().Appearance.Animation; !reflect.DeepEqual(got, want) {
		t.Errorf("defaults %+v, want %+v", got, want)
	}

	clearEnvironment(t)
	cfg, _, err := Load(writeFile(t, "appearance:\n  layout: grid\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Appearance.Animation; !reflect.DeepEqual(got, want) {
		t.Errorf("a file without the keys reads %+v, want %+v", got, want)
	}
}

// TestAnimationSources checks K2: an effect list is read from a YAML list, a
// single name and names joined by commas; the keys from the environment over
// the file
func TestAnimationSources(t *testing.T) {
	clearEnvironment(t)
	cfg, _, err := Load(writeFile(t, "appearance:\n  animation:\n"+
		"    enabled: false\n    duration: 80ms\n    step: false\n"+
		"    show: [fade, zoom]\n    hide: zoom\n    hover: fade,zoom\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := Animation{
		Enabled: false, Duration: 80 * time.Millisecond, Step: false,
		Show: []string{"fade", "zoom"}, Hide: []string{"zoom"}, Hover: []string{"fade", "zoom"},
		OverlayZoom: 0.92, HoverZoom: 1.05,
	}
	if got := cfg.Appearance.Animation; !reflect.DeepEqual(got, want) {
		t.Errorf("file: %+v, want %+v", got, want)
	}

	t.Setenv("QWS_APPEARANCE_ANIMATION_ENABLED", "true")
	t.Setenv("QWS_APPEARANCE_ANIMATION_DURATION", "300ms")
	t.Setenv("QWS_APPEARANCE_ANIMATION_STEP", "true")
	t.Setenv("QWS_APPEARANCE_ANIMATION_SHOW", "zoom")
	t.Setenv("QWS_APPEARANCE_ANIMATION_HIDE", "fade,zoom")
	t.Setenv("QWS_APPEARANCE_ANIMATION_HOVER", "none")
	cfg, _, err = Load(writeFile(t, "appearance:\n  animation:\n    show: [fade]\n    hover: [fade]\n"))
	if err != nil {
		t.Fatal(err)
	}
	want = Animation{
		Enabled: true, Duration: 300 * time.Millisecond, Step: true,
		Show: []string{"zoom"}, Hide: []string{"fade", "zoom"}, Hover: []string{"none"},
		OverlayZoom: 0.92, HoverZoom: 1.05,
	}
	if got := cfg.Appearance.Animation; !reflect.DeepEqual(got, want) {
		t.Errorf("environment: %+v, want %+v", got, want)
	}
}

// Criteria of specs/014-appearance-keys

// TestAppearanceKeys checks K5: the defaults of the header, the hover
// duration and the zoom factors keep the picture and the animations of 1.2.0;
// each key is read from a file, and from its QWS_ variable over the file
func TestAppearanceKeys(t *testing.T) {
	def := Default().Appearance
	if !def.Header.Enabled || def.Animation.HoverDuration != 0 ||
		def.Animation.OverlayZoom != 0.92 || def.Animation.HoverZoom != 1.05 {
		t.Errorf("defaults: header %+v, hover duration %v, zooms %v, %v; want shown, 0, 0.92, 1.05",
			def.Header, def.Animation.HoverDuration, def.Animation.OverlayZoom, def.Animation.HoverZoom)
	}

	clearEnvironment(t)
	file := writeFile(t, "appearance:\n  header:\n    enabled: false\n  animation:\n"+
		"    hover_duration: 90ms\n    overlay_zoom: 0.8\n    hover_zoom: 1.2\n")
	cfg, warnings, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.Appearance.Animation
	if cfg.Appearance.Header.Enabled || a.HoverDuration != 90*time.Millisecond || a.OverlayZoom != 0.8 || a.HoverZoom != 1.2 {
		t.Errorf("file: header %+v, hover duration %v, zooms %v, %v; want hidden, 90ms, 0.8, 1.2",
			cfg.Appearance.Header, a.HoverDuration, a.OverlayZoom, a.HoverZoom)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings %v, want none", warnings)
	}

	t.Setenv("QWS_APPEARANCE_HEADER_ENABLED", "true")
	t.Setenv("QWS_APPEARANCE_ANIMATION_HOVER_DURATION", "40ms")
	t.Setenv("QWS_APPEARANCE_ANIMATION_OVERLAY_ZOOM", "0.5")
	t.Setenv("QWS_APPEARANCE_ANIMATION_HOVER_ZOOM", "1.5")
	if cfg, _, err = Load(file); err != nil {
		t.Fatal(err)
	}
	a = cfg.Appearance.Animation
	if !cfg.Appearance.Header.Enabled || a.HoverDuration != 40*time.Millisecond || a.OverlayZoom != 0.5 || a.HoverZoom != 1.5 {
		t.Errorf("environment: header %+v, hover duration %v, zooms %v, %v; want shown, 40ms, 0.5, 1.5",
			cfg.Appearance.Header, a.HoverDuration, a.OverlayZoom, a.HoverZoom)
	}
}

// changedConfig is a configuration with every field different from its
// default
func changedConfig(t *testing.T) *Config {
	cfg := &Config{
		Keybindings: Keybindings{Modifier: "Super", Key: "grave", Backward: "Ctrl", WorkspaceModifier: "Shift", Cancel: "q"},
		Appearance: Appearance{
			Layout:      "grid",
			Renderer:    "none", // neither default, cpu here or glx after 001
			Thumbnail:   Thumbnail{Width: 300, Height: 200, ScalingAlgorithm: "nearest"},
			Spacing:     450,
			Perspective: 0.5,
			Grid:        Grid{Columns: 4, Spacing: 12},
			Shadow:      Shadow{Offset: 3, Blur: 4},
			Font:        Font{Paths: []string{"/tmp/a.ttf"}, Size: 17},
			Colors: Colors{
				Theme: "dark",
				Dark:  ThemeColor{"#000001", "#000002", "#000003", "#000004", "#000005", "#000006"},
				Light: ThemeColor{"#100001", "#100002", "#100003", "#100004", "#100005", "#100006"},
			},
			WindowBackground: WindowBackground{Enabled: false, Opacity: 0.5, BorderRadius: 7},
			WindowPadding:    WindowPadding{Horizontal: "5%", Vertical: "6%"},
			Header:           Header{Enabled: false},
			Animation: Animation{
				Enabled: false, Duration: 300 * time.Millisecond, Step: false,
				Show: []string{"zoom"}, Hide: []string{"fade"}, Hover: []string{},
				HoverDuration: 70 * time.Millisecond, OverlayZoom: 0.8, HoverZoom: 1.2,
			},
		},
		Behavior: Behavior{SnapshotInterval: 1500 * time.Millisecond, ShowDelay: 20 * time.Millisecond},
		Windows:  Windows{Workspace: "current", IgnoreSkipTaskbar: true, SortMinimizedLast: true},
		Log:      Log{Level: "debug", Format: "json"},
	}
	def := reflect.ValueOf(*Default())
	var same func(a, b reflect.Value, path string)
	same = func(a, b reflect.Value, path string) {
		if a.Kind() == reflect.Struct {
			for i := 0; i < a.NumField(); i++ {
				same(a.Field(i), b.Field(i), path+"."+a.Type().Field(i).Name)
			}
			return
		}
		if reflect.DeepEqual(a.Interface(), b.Interface()) {
			t.Fatalf("changedConfig: %s equals its default", path)
		}
	}
	same(reflect.ValueOf(*cfg), def, "Config")
	return cfg
}

// unjoined sets every field of want under a multi-word name — one config
// init wrote joined — to its value in def
func unjoined(want, def reflect.Value) {
	for i := 0; i < want.NumField(); i++ {
		f := want.Type().Field(i)
		if strings.ToLower(f.Name) != f.Tag.Get("mapstructure") {
			want.Field(i).Set(def.Field(i))
		} else if f.Type.Kind() == reflect.Struct {
			unjoined(want.Field(i), def.Field(i))
		}
	}
}

// joined is v as yaml.v3 marshals it without tags: fields named by their
// lowercased Go names
func joined(v reflect.Value) any {
	if v.Kind() != reflect.Struct {
		return v.Interface()
	}
	m := map[string]any{}
	for i := 0; i < v.NumField(); i++ {
		m[strings.ToLower(v.Type().Field(i).Name)] = joined(v.Field(i))
	}
	return m
}

func writeFile(t *testing.T, content string) string {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// clearEnvironment hides the QWS_* variables of the environment the tests
// run in; Viper treats an empty variable as unset
func clearEnvironment(t *testing.T) {
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "QWS_") {
			t.Setenv(name, "")
		}
	}
}
