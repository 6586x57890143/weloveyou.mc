package discord

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Icons is icons.toml: the 8x8 pixel set, shared with scripts/pixelicons.py.
//
// Discord cannot render SVG in a message, so the same grids the published pages
// draw inline have to arrive as uploaded images. Reading one file from both
// languages is what stops the two drifting, which is the mistake the shared
// palette was extracted to fix and would have been repeated here.
type Icons struct {
	Palette map[string]string `toml:"palette"`
	Icons   map[string]struct {
		Rows []string `toml:"rows"`
	} `toml:"icons"`
}

// LoadIcons reads and validates the set.
func LoadIcons(path string) (*Icons, error) {
	var ic Icons
	if _, err := toml.DecodeFile(path, &ic); err != nil {
		return nil, fmt.Errorf("icons: %w", err)
	}
	if len(ic.Icons) == 0 {
		return nil, fmt.Errorf("icons: %s declares none", path)
	}
	for name, hex := range ic.Palette {
		if _, err := parseHex(hex); err != nil {
			return nil, fmt.Errorf("icons: palette %q: %w", name, err)
		}
	}
	for name, ico := range ic.Icons {
		if len(ico.Rows) == 0 {
			return nil, fmt.Errorf("icons: %q has no rows", name)
		}
		w := len(ico.Rows[0])
		for i, r := range ico.Rows {
			if len(r) != w {
				return nil, fmt.Errorf("icons: %q row %d is %d wide, row 0 is %d; "+
					"a ragged grid renders as a torn image", name, i, len(r), w)
			}
		}
	}
	return &ic, nil
}

// Names returns the icon names, for callers that want to check one exists.
func (ic *Icons) Names() []string {
	out := make([]string, 0, len(ic.Icons))
	for n := range ic.Icons {
		out = append(out, n)
	}
	return out
}

// PNG renders one icon at the given size, nearest-neighbour so the pixels stay
// square. Discord wants 128x128 for an emoji and re-encodes anyway, but a
// smooth-scaled 8x8 would arrive as a blur, which is the opposite of the point.
//
// A character with no palette entry is transparent. That is how the heart gets
// its shape rather than sitting in a coloured box.
func (ic *Icons) PNG(name string, size int) ([]byte, error) {
	ico, ok := ic.Icons[name]
	if !ok {
		return nil, fmt.Errorf("icons: no icon named %q", name)
	}
	h := len(ico.Rows)
	w := len(ico.Rows[0])
	if size < h || size < w {
		return nil, fmt.Errorf("icons: size %d is smaller than the %dx%d grid", size, w, h)
	}
	sx, sy := size/w, size/h

	img := image.NewNRGBA(image.Rect(0, 0, w*sx, h*sy))
	for y, row := range ico.Rows {
		for x, ch := range row {
			hex, ok := ic.Palette[string(ch)]
			if !ok {
				continue // transparent, and the zero value already is
			}
			rgb, err := parseHex(hex)
			if err != nil {
				return nil, err
			}
			c := color.NRGBA{
				R: uint8(rgb >> 16), G: uint8(rgb >> 8), B: uint8(rgb), A: 0xFF,
			}
			for dy := 0; dy < sy; dy++ {
				for dx := 0; dx < sx; dx++ {
					img.Set(x*sx+dx, y*sy+dy, c)
				}
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DataURI is what Discord's emoji endpoint takes for an image.
func (ic *Icons) DataURI(name string, size int) (string, error) {
	raw, err := ic.PNG(name, size)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw), nil
}

func parseHex(s string) (int, error) {
	h := strings.TrimPrefix(s, "#")
	if len(h) != 6 {
		return 0, fmt.Errorf("colour %q is not #RRGGBB", s)
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return 0, fmt.Errorf("colour %q is not hexadecimal", s)
	}
	return int(v), nil
}
