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

PALETTE = {
    "h": "#3A2A16",  # hair
    "s": "#B58868",  # skin
    "p": "#C8A882",  # villager skin, paler
    "n": "#A97B57",  # that nose
    "w": "#E8E8E8",  # eye white
    "b": "#3A5FCD",  # eye
    "m": "#6E4A32",  # mouth
    "f": "#7A7A7A",  # machine frame
    "d": "#3A3A3A",  # machine interior
    "g": "#D8A657",  # a lit panel, and gold
    "y": "#A87A2E",  # gold, in shadow
    "G": "#6A8F3C",  # grass
    "D": "#79553A",  # dirt
    "H": "#E39AAE",  # heart
    "o": "#7A5C3A",  # tool handle
    "i": "#C9CCD1",  # iron
    "P": "#D8C9A3",  # parchment
    "r": "#C4705C",  # a route drawn on it, and anything gone wrong
    "k": "#E8E4D8",  # bone
    "x": "#2A2622",  # a hole
}

# A character with no palette entry is transparent, which is what "." means
# everywhere below.
ICONS = {
    # Steve, more or less: hair, eyes, a suggestion of a mouth.
    "player": [
        "hhhhhhhh",
        "hhhhhhhh",
        "hssssssh",
        "swbssbws",
        "ssssssss",
        "sssmmsss",
        "ssssssss",
        "ssssssss",
    ],
    # The villager is all brow and nose, which is the whole joke.
    "villager": [
        "hhhhhhhh",
        "hhhhhhhh",
        "pppppppp",
        "pwpnnpwp",
        "pppnnppp",
        "pppnnppp",
        "pppnnppp",
        "pppppppp",
    ],
    # A machine: metal frame, lit face.
    "machines": [
        "ffffffff",
        "fddddddf",
        "fdggggdf",
        "fdgddgdf",
        "fdgddgdf",
        "fdggggdf",
        "fddddddf",
        "ffffffff",
    ],
    # Grass on dirt, for the worldgen workloads.
    "world": [
        "GGGGGGGG",
        "GGGGGGGG",
        "DGDDGDDG",
        "DDDDDDDD",
        "DDDDDDDD",
        "DDDDDDDD",
        "DDDDDDDD",
        "DDDDDDDD",
    ],
    # The server's own mark. It is in the MOTD, so it belongs here too.
    "heart": [
        ".HH..HH.",
        "HHHHHHHH",
        "HHHHHHHH",
        "HHHHHHHH",
        ".HHHHHH.",
        "..HHHH..",
        "...HH...",
        "........",
    ],
    # Bring a shovel. Drawn head-on rather than on the diagonal: at the 18px an
    # inline emoji actually renders at, a diagonal handle reads as a smudge.
    # Checked in a browser, which is the only way that shows up.
    "shovel": [
        "...oo...",
        "...oo...",
        "...oo...",
        "..iiii..",
        ".iiiiii.",
        ".iiiiii.",
        ".iiiiii.",
        "..iiii..",
    ],
    "map": [
        "PPPPPPPP",
        "PPPPPPPP",
        "PPrrPPPP",
        "PPPPrPPP",
        "PPPrPPPP",
        "PPPPrrPP",
        "PPPPPPPP",
        "PPPPPPPP",
    ],
    # Spend, on the daily cost post.
    "coin": [
        "..gggg..",
        ".gggggg.",
        "gggggggg",
        "ggyyyygg",
        "ggyyyygg",
        "gggggggg",
        ".gggggg.",
        "..gggg..",
    ],
    # A death in the feed.
    "skull": [
        ".kkkkkk.",
        "kkkkkkkk",
        "kxxkkxxk",
        "kxxkkxxk",
        "kkkkkkkk",
        "kkxxxxkk",
        ".kxkkxk.",
        "..kkkk..",
    ],
}


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
