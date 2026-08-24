package discord

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The real file is the one that matters, so it is a test input rather than a
// fixture. A change to guild.toml that breaks its own rules fails here.
func TestLoadRealConfig(t *testing.T) {
	g, err := Load(filepath.Join("..", "..", "guild.toml"))
	if err != nil {
		t.Fatalf("guild.toml does not load: %v", err)
	}
	if g.Meta.Name != "wly" {
		t.Errorf("name = %q, want wly", g.Meta.Name)
	}
	if len(g.Roles) == 0 || len(g.Channels) == 0 {
		t.Fatalf("got %d roles and %d channels, want both non-empty",
			len(g.Roles), len(g.Channels))
	}
	// Order is the hierarchy. admin outranks player, and if that ever silently
	// inverts the whole permission model is wrong.
	if g.Roles[0].Name != "admin" {
		t.Errorf("highest role = %q, want admin", g.Roles[0].Name)
	}
	if _, err := g.Surfaces(); err != nil {
		t.Errorf("surfaces conflict: %v", err)
	}
}

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "guild.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadRejects(t *testing.T) {
	const head = "[guild]\nname = \"wly\"\nid = \"1\"\n"
	for _, tc := range []struct{ name, body, want string }{
		{"no name", "[guild]\nname = \"\"\n", "name is empty"},
		{"role without name", head + "[[roles]]\ncolor = \"#FFFFFF\"\n", "has no name"},
		{"duplicate role", head +
			"[[roles]]\nname = \"a\"\n[[roles]]\nname = \"a\"\n", "declared twice"},
		{"bad colour", head +
			"[[roles]]\nname = \"a\"\ncolor = \"red\"\n", "not #RRGGBB"},
		{"bad hex", head +
			"[[roles]]\nname = \"a\"\ncolor = \"#GGGGGG\"\n", "not hexadecimal"},
		{"tertiary without secondary", head +
			"[[roles]]\nname = \"a\"\ncolor = \"#FFFFFF\"\n" +
			"colors = { primary = \"#FFFFFF\", tertiary = \"#000000\" }\n", "which Discord ignores"},
		{"gradient without flat fallback", head +
			"[[roles]]\nname = \"a\"\ncolors = { primary = \"#FFFFFF\", secondary = \"#000000\" }\n",
			"no flat color"},
		{"bad gradient colour", head +
			"[[roles]]\nname = \"a\"\ncolor = \"#FFFFFF\"\n" +
			"colors = { primary = \"nope\", secondary = \"#000000\" }\n", "colors.primary"},
		{"channel without name", head + "[[channels]]\ntopic = \"x\"\n", "has no name"},
		{"duplicate channel", head +
			"[[channels]]\nname = \"a\"\n[[channels]]\nname = \"a\"\n", "declared twice"},
		{"uppercase channel", head + "[[channels]]\nname = \"General\"\n", "must be lowercase"},
		{"spaced channel", head + "[[channels]]\nname = \"a b\"\n", "must be lowercase"},
		{"visible_to unknown role", head +
			"[[channels]]\nname = \"a\"\nvisible_to = [\"ghost\"]\n", "not a declared role"},
		{"unparseable", "[guild\n", "guild config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(write(t, tc.body))
			if err == nil {
				t.Fatalf("accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent.toml")); err == nil {
		t.Fatal("loaded a file that does not exist")
	}
}

func TestSurfacesConflict(t *testing.T) {
	g := &Guild{Channels: []Channel{
		{Name: "a", Surface: "status"},
		{Name: "b", Surface: "status"},
	}}
	if _, err := g.Surfaces(); err == nil {
		t.Fatal("accepted one surface claimed by two channels")
	}
}

func TestCategoriesKeepFileOrder(t *testing.T) {
	g := &Guild{Channels: []Channel{
		{Name: "a", Category: "second"},
		{Name: "b", Category: "first"},
		{Name: "c", Category: "second"},
		{Name: "d"}, // no category, must not appear
	}}
	got := g.Categories()
	want := []string{"second", "first"}
	if len(got) != len(want) {
		t.Fatalf("categories = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("categories = %v, want %v", got, want)
		}
	}
}

func TestParseColor(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"", 0, false},
		{"#E39AAE", 0xE39AAE, false},
		{"E39AAE", 0xE39AAE, false},
		{"#000000", 0, false},
		{"#FFFFFF", 0xFFFFFF, false},
		{"#FFF", 0, true},
		{"#GGGGGG", 0, true},
		{"nope", 0, true},
	} {
		got, err := ParseColor(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("ParseColor(%q) error = %v, wantErr %t", tc.in, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && got != tc.want {
			t.Errorf("ParseColor(%q) = %#X, want %#X", tc.in, got, tc.want)
		}
	}
}

// The heart is the one colour that appears in three places. If it ever stops
// agreeing with scripts/brand.py this is where it shows up.
func TestHeartMatchesBrand(t *testing.T) {
	const heart = 0xE39AAE
	got, err := ParseColor("#E39AAE")
	if err != nil || got != heart {
		t.Fatalf("ParseColor(heart) = %#X, %v", got, err)
	}
}
