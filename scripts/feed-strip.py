#!/usr/bin/env python3
"""Draw the feed strip: a pink rule with the pixel heart in the middle.

    python scripts/feed-strip.py --out assets/feed-strip.png

WHY THIS IMAGE EXISTS, because it looks like decoration and is not.

A Components V2 Container has NO width property. It takes the width of its
widest child, capped at the message column, so a feed of events renders ragged:
measured in Discord's own message font, "earned Stone Age" is 152px and "joined
for the first time" is 184px, and no amount of text wrangling makes those agree.
Three fixes were tried against the live guild and all three failed:

  a run of U+2007 FIGURE SPACE   STRIPPED. Discord removes it from message
                                 content; the posted cards read back with none
  a Separator on every card      no effect, a separator has no intrinsic width
  a Section with an accessory    no effect, the button sits after the text

A Media Gallery image is the ONLY component with an intrinsic width, because an
image has real dimensions. Every card carrying the same image is therefore
exactly as wide as that image, which is what makes the feed uniform. Verified on
the live guild before this file was written.

So the strip is load-bearing layout that also happens to carry the brand, rather
than an ornament that happens to be there. It doubles as a divider anywhere else
a rule is wanted.

The heart and the palette are read from icons.toml, the same file the reconciler
uploads emoji from and pixelicons.py draws the web icons from. One source, so
the strip cannot drift from the heart people see inline.
"""

import argparse
import pathlib
import struct
import sys
import tomllib
import zlib

# The strip is wider than the message column on purpose. Discord scales an
# oversized image down to the column and every card lands on the same width;
# an image NARROWER than the column would leave the cards at its own width and
# waste the space. 640 is comfortably past the widest column Discord renders.
WIDTH = 640
HEIGHT = 16

# Scale for the 8x8 heart. 2 gives a 16px heart, which is the strip's height and
# reads as a heart rather than a smudge at the size Discord renders it.
SCALE = 2

# Clear space either side of the heart, so the rule does not run into it.
GAP = 5


def load_icon(icons_path, name):
    """The heart grid and the palette, from the one file that holds both."""
    data = tomllib.loads(pathlib.Path(icons_path).read_text(encoding="utf-8"))
    palette = data["palette"]
    rows = data["icons"][name]["rows"]
    return rows, palette


def rgba(hex_colour, alpha=255):
    h = hex_colour.lstrip("#")
    return (int(h[0:2], 16), int(h[2:4], 16), int(h[4:6], 16), alpha)


def draw(rows, palette):
    """Return the strip as a list of rows of RGBA tuples."""
    clear = (0, 0, 0, 0)
    px = [[clear for _ in range(WIDTH)] for _ in range(HEIGHT)]

    heart_w = len(rows[0]) * SCALE
    heart_h = len(rows) * SCALE
    x0 = (WIDTH - heart_w) // 2
    y0 = (HEIGHT - heart_h) // 2

    # The rule: two pixels, vertically centred, stopping either side of the
    # heart. Dimmer than the heart so the heart is what the eye lands on.
    rule = rgba(palette["H"], 150)
    for y in (HEIGHT // 2 - 1, HEIGHT // 2):
        for x in range(WIDTH):
            if x0 - GAP <= x < x0 + heart_w + GAP:
                continue
            px[y][x] = rule

    # The heart itself, at full strength.
    for ry, row in enumerate(rows):
        for rx, key in enumerate(row):
            if key == ".":
                continue
            colour = rgba(palette[key])
            for dy in range(SCALE):
                for dx in range(SCALE):
                    y, x = y0 + ry * SCALE + dy, x0 + rx * SCALE + dx
                    if 0 <= y < HEIGHT and 0 <= x < WIDTH:
                        px[y][x] = colour
    return px


def png(px):
    """Encode RGBA rows as a PNG. Stdlib only, like every script here."""
    raw = b"".join(
        b"\x00" + b"".join(struct.pack("BBBB", *p) for p in row) for row in px
    )

    def chunk(tag, payload):
        body = tag + payload
        return struct.pack(">I", len(payload)) + body + struct.pack(">I", zlib.crc32(body))

    return (
        b"\x89PNG\r\n\x1a\n"
        + chunk(b"IHDR", struct.pack(">IIBBBBB", WIDTH, HEIGHT, 8, 6, 0, 0, 0))
        + chunk(b"IDAT", zlib.compress(raw, 9))
        + chunk(b"IEND", b"")
    )


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--icons", default="icons.toml")
    ap.add_argument("--out", default="assets/feed-strip.png")
    ap.add_argument("--check", action="store_true",
                    help="render and report, write nothing")
    args = ap.parse_args()

    rows, palette = load_icon(args.icons, "heart")
    data = png(draw(rows, palette))

    if args.check:
        print(f"{WIDTH}x{HEIGHT}, {len(data)} bytes")
        return 0

    out = pathlib.Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_bytes(data)
    print(f"wrote {out} ({WIDTH}x{HEIGHT}, {len(data)} bytes)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
