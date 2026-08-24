"""The 8x8 pixel icon set, shared by every page and surface this project ships.

8x8 because that is exactly how big a head is in a Minecraft skin file. Drawn as
inline SVG rects rather than an emoji or an image file: a page ships as one
self-contained file with no external assets, and a run of same-coloured pixels
collapses into a single rect, so each icon costs a few hundred bytes.

Two consumers, one source, because they were about to diverge:

    bench-site.py     renders them as inline SVG in the results page
    discord-mocks.py  renders them as inline SVG in the mockups, standing in for
                      the custom guild emoji Discord will actually show

**Discord cannot render SVG.** In a real message these are custom guild emoji:
the same grids uploaded as 128x128 images and written inline as `<:name:id>`.
Uploading them is the reconciler's job (phase D1) and needs a PNG encoder that
does not exist yet. Until then the mockups draw the SVG so what you review is
what a player will see.
"""

import pathlib
import tomllib

# ONE SOURCE. The grids moved to icons.toml so internal/discord can read them
# too: Discord cannot render SVG in a message, so the same icons have to arrive
# there as uploaded PNG, and a second copy in Go would have drifted exactly the
# way the palette did before scripts/brand.py existed.
_DATA = tomllib.loads(
    (pathlib.Path(__file__).resolve().parent.parent / "icons.toml").read_text(encoding="utf-8"))

PALETTE = _DATA["palette"]
ICONS = {name: entry["rows"] for name, entry in _DATA["icons"].items()}


def svg(name, size=20, cls="icon"):
    """One icon as inline SVG, runs of colour collapsed into rects.

    Returns "" for an unknown name rather than raising or drawing a wrong icon,
    so a surface that names an icon that does not exist renders without one.
    """
    rows = ICONS.get(name)
    if not rows:
        return ""
    rects = []
    for y, row in enumerate(rows):
        x = 0
        while x < len(row):
            run = 1
            while x + run < len(row) and row[x + run] == row[x]:
                run += 1
            fill = PALETTE.get(row[x])
            if fill:
                rects.append(
                    '<rect x="%d" y="%d" width="%d" height="1" fill="%s"/>'
                    % (x, y, run, fill))
            x += run
    return ('<svg class="%s" viewBox="0 0 8 8" width="%d" height="%d" '
            'aria-hidden="true" focusable="false">%s</svg>'
            % (cls, size, size, "".join(rects)))
