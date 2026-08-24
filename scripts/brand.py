"""The wly visual identity, in one place.

Every page this project publishes opens the same way and is built from the same
tokens. That used to be a convention held by copy-paste, and it drifted: the
Discord mockups grew a parallel set of token names (`--page` where everything
else says `--bg`) with identical values, so the next person to copy a rule would
have copied the wrong one, and the mono stack quietly lost a fallback.

    bench-site.py     the benchmark results page
    discord-mocks.py  the Discord surface mockups
    pack-site.py      lives in weloveyou-pack, so it cannot import this. It
                      carries a copy and a comment naming this file as the
                      source of truth. One duplicated block across a repo
                      boundary, deliberately, the same way the pack URL is.

## The name

`wly` everywhere a person sees a name. The long form is the project's origin,
not its label.

## The heart, which is three glyphs on purpose

| where | glyph | why |
|---|---|---|
| web pages | `\U0001f496` | full colour, renders everywhere a browser does |
| Discord | `<:heart:>` | a custom guild emoji from pixelicons.py, so it matches the rest of the icon set |
| Minecraft MOTD | `♥` | Minecraft's default font has no emoji, and an unrenderable glyph shows as a box |

This is not drift. Each medium gets the heart it can actually draw.
"""

# The heart, per medium. Named so nobody has to remember which is which.
HEART_WEB = "\U0001f496"
HEART_DISCORD = "<:heart:>"
HEART_MOTD = "♥"

# Minecraft's own chat palette, desaturated for reading on a dark background:
#
#   §a #55FF55 -> --win   better than baseline
#   §c #FF5555 -> --lose  worse than baseline, and failures
#   §6 #FFAA00 -> --base  the baseline row, warnings
#   §b #55FFFF -> --info  links
#   §7 #AAAAAA -> --dim   secondary text
#
# In a Discord message none of this is available: Container.accent_color is the
# only colour lever, so these become accent values instead. See docs/DISCORD.md.
PALETTE = """:root{
 --bg:#211F1B; --panel:#282621; --rule:#3B372F; --rule-hi:#564E42;
 --fg:#D5CEC1; --fg-hi:#EFE9DC; --dim:#8E8677;
 --win:#8FA860; --lose:#C4705C; --base:#D8A657; --info:#84A69C;
 --heart:#E39AAE;
 --mono:ui-monospace,"Cascadia Code","JetBrains Mono",Menlo,Consolas,"DejaVu Sans Mono",monospace;
}"""

# The header every page opens with. The heart belongs on the page, not only in
# the browser tab, so the name carries it and the pink is a real token.
BRAND_CSS = """.bar-row{display:flex;align-items:baseline;gap:1ch;padding-bottom:.5rem}
.bar-row .t{flex:1 1 auto;display:flex;align-items:baseline;gap:.9ch;flex-wrap:wrap}
.bar-row .d{color:var(--dim);letter-spacing:.04em}
.brand{color:var(--heart);font-size:19px;font-weight:600;letter-spacing:.02em}
.brand .hb{font-size:16px}
.wordmark{color:var(--dim);letter-spacing:.14em;text-transform:uppercase;font-size:13px}
footer .hb{color:var(--heart)}"""


def title(what):
    """The browser-tab title. One pattern for every page: `wly HEART <what>`."""
    return "wly %s %s" % (HEART_WEB, what)


def bar(subtitle, right="", name="wly"):
    """The header block: the name and its heart, then what the page is.

    Arguments are escaped, so pass real characters and not HTML entities. A
    `&middot;` handed to this rendered as a literal `&MIDDOT;` across the top of
    a published page, which nothing in the source hinted at.
    """
    import html as _html
    for arg in (subtitle, right, name):
        if "&" in arg and ";" in arg:
            raise ValueError("brand.bar escapes its arguments; pass a real "
                             "character, not an HTML entity: %r" % arg)
    return (
        '<div class="bar-row"><span class="t">'
        '<span class="brand">%s <span class="hb">%s</span></span>'
        '<span class="wordmark">%s</span></span>'
        '<span class="d">%s</span></div>'
        '<div class="hrule"></div>'
        % (_html.escape(name), HEART_WEB, _html.escape(subtitle), _html.escape(right))
    )
