package discord

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func realIcons(t *testing.T) *Icons {
	t.Helper()
	ic, err := LoadIcons(filepath.Join("..", "..", "icons.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return ic
}

// The real file is the input, so an edit that breaks its own rules fails here.
func TestLoadRealIcons(t *testing.T) {
	ic := realIcons(t)
	if len(ic.Icons) < 9 {
		t.Errorf("only %d icons", len(ic.Icons))
	}
	for _, want := range []string{"heart", "shovel", "map", "coin", "skull"} {
		if _, ok := ic.Icons[want]; !ok {
			t.Errorf("icons.toml has no %q, which a surface references", want)
		}
	}
	if len(ic.Names()) != len(ic.Icons) {
		t.Error("Names() disagrees with the map")
	}
}

func TestPNGIsSquareAndSized(t *testing.T) {
	ic := realIcons(t)
	raw, err := ic.PNG("heart", 128)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("not a decodable PNG: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != 128 || b.Dy() != 128 {
		t.Errorf("size = %dx%d, want 128x128", b.Dx(), b.Dy())
	}
	// Discord's cap is 256 KiB. An 8x8 scaled up is nowhere near it, and if it
	// ever were, the upload would fail on a live server instead of here.
	if len(raw) > 256*1024 {
		t.Errorf("%d bytes, over Discord's 256 KiB emoji cap", len(raw))
	}
}

// The heart is a shape, not a coloured square. If the transparent character
// ever starts painting, every icon becomes a block.
func TestUnpaletteCharsAreTransparent(t *testing.T) {
	ic := realIcons(t)
	raw, err := ic.PNG("heart", 128)
	if err != nil {
		t.Fatal(err)
	}
	img, _ := png.Decode(bytes.NewReader(raw))
	// Top-left of the heart grid is ".", so it must be fully transparent.
	if _, _, _, a := img.At(2, 2).RGBA(); a != 0 {
		t.Errorf("top-left alpha = %d, want 0", a)
	}
	// The middle of row 1 is "H", so it must be opaque heart pink.
	r, g, b, a := img.At(64, 24).RGBA()
	if a == 0 {
		t.Fatal("the heart body is transparent")
	}
	if r>>8 != 0xE3 || g>>8 != 0x9A || b>>8 != 0xAE {
		t.Errorf("heart colour = #%02X%02X%02X, want #E39AAE", r>>8, g>>8, b>>8)
	}
}

func TestDataURI(t *testing.T) {
	ic := realIcons(t)
	uri, err := ic.DataURI("skull", 128)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uri, "data:image/png;base64,") {
		t.Errorf("uri = %.40s", uri)
	}
	if _, err := ic.DataURI("nope", 128); err == nil {
		t.Error("made a data URI for an icon that does not exist")
	}
}

func TestPNGRejects(t *testing.T) {
	ic := realIcons(t)
	if _, err := ic.PNG("nope", 128); err == nil {
		t.Error("rendered an unknown icon")
	}
	if _, err := ic.PNG("heart", 4); err == nil {
		t.Error("rendered smaller than the grid")
	}
}

func writeIcons(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "icons.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadIconsRejects(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"no icons", "[palette]\n\"a\" = \"#FFFFFF\"\n", "declares none"},
		{"bad palette colour", "[palette]\n\"a\" = \"red\"\n[icons.x]\nrows = [\"a\"]\n", "not #RRGGBB"},
		{"no rows", "[icons.x]\nrows = []\n", "has no rows"},
		{"ragged grid", "[icons.x]\nrows = [\"aa\", \"a\"]\n", "torn image"},
		{"unparseable", "[icons\n", "icons:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadIcons(writeIcons(t, tc.body))
			if err == nil {
				t.Fatalf("accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	if _, err := LoadIcons(filepath.Join(t.TempDir(), "absent.toml")); err == nil {
		t.Error("loaded a file that does not exist")
	}
}

// Every emoji guild.toml wants to upload must actually exist in icons.toml, or
// apply fails partway through against a live server.
func TestDeclaredEmojiAllExist(t *testing.T) {
	g, err := Load(filepath.Join("..", "..", "guild.toml"))
	if err != nil {
		t.Fatal(err)
	}
	ic := realIcons(t)
	for _, name := range g.Emojis.Upload {
		if _, ok := ic.Icons[name]; !ok {
			t.Errorf("guild.toml uploads %q, which icons.toml does not define", name)
		}
	}
}
