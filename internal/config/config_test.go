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

// TestJoinedKeys checks K4: a file written with joined names, as config init
// wrote it, is read with a warning per joined key, and a key with
// underscores wins over its joined form
func TestJoinedKeys(t *testing.T) {
	clearEnvironment(t)
	want := changedConfig(t)
	data, err := yaml.Marshal(joined(reflect.ValueOf(*want)))
	if err != nil {
		t.Fatal(err)
	}

	got, warnings, err := Load(writeFile(t, string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("read\n%+v\nwritten\n%+v", got, want)
	}
	if n := len(joinedKeys()); len(warnings) != n {
		t.Errorf("%d warnings, want %d: %v", len(warnings), n, warnings)
	}
	for _, w := range warnings {
		if !strings.Contains(w, "rename it") {
			t.Errorf("warning %q does not say what to do", w)
		}
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

// TestAnimationDefaults checks K1: the defaults the author chose
func TestAnimationDefaults(t *testing.T) {
	want := Animation{
		Enabled: true, Duration: 150 * time.Millisecond, Step: true,
		Show: []string{"fade", "zoom"}, Hide: []string{"fade", "zoom"}, Hover: []string{"fade", "zoom"},
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
	}
	if got := cfg.Appearance.Animation; !reflect.DeepEqual(got, want) {
		t.Errorf("environment: %+v, want %+v", got, want)
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
			Animation: Animation{
				Enabled: false, Duration: 300 * time.Millisecond, Step: false,
				Show: []string{"zoom"}, Hide: []string{"fade"}, Hover: []string{},
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
