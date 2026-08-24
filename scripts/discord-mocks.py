#!/usr/bin/env python3
"""Render the committed Components V2 payloads into one comparable page.

    scripts/discord-mocks.py --out mocks.html
    scripts/discord-mocks.py --check          # validate only, write nothing

The mockups ARE the payloads. Each file under internal/discord/testdata/surfaces
is the exact JSON the bot will POST, so a design here cannot promise a layout
Components V2 refuses to build, and the golden files CLAUDE.md's testing section
asks for exist before any Go does.

Validation runs on every render, because a payload that renders beautifully here
and 400s at Discord is the same failure as a JVM flag the JVM accepts and then
ignores: plausible, and measuring nothing.
"""

from __future__ import annotations

import argparse
import base64
import datetime
import html
import json
import mimetypes
import re
import sys
from pathlib import Path

from brand import BRAND_CSS, PALETTE, title
from brand import bar as brandbar
from pixelicons import ICONS, svg

ROOT = Path(__file__).resolve().parent.parent
SURFACES = ROOT / "internal" / "discord" / "testdata" / "surfaces"

# Images the surfaces link to live in the pack repo and are published from
# there, so this page cannot fetch them: it has to be self-contained, and the
# URL is not live until a pack release. Point --assets at a directory and any
# media whose basename matches is inlined as a data URI. Unset (which is how CI
# runs) draws the placeholder instead, which is honest rather than broken.
ASSETS = None

# Components V2, as of the current Discord component reference. Every number
# here is a limit the API enforces, not a style preference.
FLAG_IS_COMPONENTS_V2 = 1 << 15          # 32768, and irreversible once set
MAX_TOP_LEVEL = 10
MAX_TOTAL = 30                           # the reference says 40, the changelog 30
MAX_TEXT_CHARS = 4000
MAX_BUTTONS_PER_ROW = 5

ACTION_ROW, BUTTON, SECTION = 1, 2, 9
TEXT_DISPLAY, THUMBNAIL, MEDIA_GALLERY, FILE, SEPARATOR = 10, 11, 12, 13, 14

# Discord's timestamp styles. Anything else renders as literal text to a player,
# which looks like a bug rather than a date.
TS_STYLES = {
    "t": "short time", "T": "long time", "d": "short date", "D": "long date",
    "f": "short date/time", "F": "long date/time", "R": "relative",
}
CONTAINER = 17

NAMES = {
    ACTION_ROW: "Action Row", BUTTON: "Button", SECTION: "Section",
    TEXT_DISPLAY: "Text Display", THUMBNAIL: "Thumbnail",
    MEDIA_GALLERY: "Media Gallery", FILE: "File", SEPARATOR: "Separator",
    CONTAINER: "Container",
}

# ---------------------------------------------------------------------------
# Validation
# ---------------------------------------------------------------------------


def walk(components):
    """Yield every component in the tree, accessories included."""
    for c in components or []:
        yield c
        yield from walk(c.get("components"))
        if c.get("accessory"):
            yield c["accessory"]


def validate(payload, label):
    """Return a list of human-readable errors. Empty means Discord would take it."""
    errs = []

    def err(msg):
        errs.append(f"{label}: {msg}")

    flags = payload.get("flags", 0)
    if not flags & FLAG_IS_COMPONENTS_V2:
        err(f"flags missing IS_COMPONENTS_V2 ({FLAG_IS_COMPONENTS_V2}); "
            "without it Discord reads this as a legacy message")
    for legacy in ("content", "embeds", "poll", "stickers"):
        if payload.get(legacy):
            err(f"`{legacy}` is set, and Components V2 disables it")

    top = payload.get("components") or []
    if not top:
        err("no components")
    if len(top) > MAX_TOP_LEVEL:
        err(f"{len(top)} top-level components, max {MAX_TOP_LEVEL}")

    every = list(walk(top))
    if len(every) > MAX_TOTAL:
        err(f"{len(every)} components in total, max {MAX_TOTAL}")

    text = sum(len(c.get("content", "")) for c in every if c.get("type") == TEXT_DISPLAY)
    if text > MAX_TEXT_CHARS:
        err(f"{text} characters of text, max {MAX_TEXT_CHARS}")

    for c in every:
        t = c.get("type")
        if t not in NAMES:
            err(f"unknown component type {t!r}")
            continue

        if t == SECTION:
            kids = c.get("components") or []
            if not 1 <= len(kids) <= 3:
                err(f"Section has {len(kids)} children, must be 1-3")
            if any(k.get("type") != TEXT_DISPLAY for k in kids):
                err("Section children must all be Text Displays")
            acc = c.get("accessory")
            if not acc:
                err("Section has no accessory; one is required")
            elif acc.get("type") not in (BUTTON, THUMBNAIL):
                err(f"Section accessory is {NAMES.get(acc.get('type'), acc.get('type'))}, "
                    "must be a Button or a Thumbnail")

        elif t == BUTTON:
            if c.get("custom_id"):
                err(f"button {c.get('label')!r} has a custom_id, which commits the bot "
                    "to an interaction handler; use a link button (style 5) in a design pass")
            if c.get("style") == 5 and not c.get("url"):
                err(f"link button {c.get('label')!r} has no url")

        elif t == ACTION_ROW:
            kids = c.get("components") or []
            if len(kids) > MAX_BUTTONS_PER_ROW:
                err(f"Action Row has {len(kids)} buttons, max {MAX_BUTTONS_PER_ROW}")

        elif t == TEXT_DISPLAY:
            if not c.get("content", "").strip():
                err("Text Display has no content")
            for ts in re.findall(r"<t:\d+:(\w)>", c.get("content", "")):
                if ts not in TS_STYLES:
                    err("uses timestamp style :%s:, which Discord does not "
                        "render; valid styles are %s" % (ts, ", ".join(sorted(TS_STYLES))))
            for ref in re.findall(r"<:(\w+):\d*>", c.get("content", "")):
                if ref not in ICONS:
                    err("names custom emoji :%s:, which is not in "
                        "scripts/pixelicons.py" % ref)
            if re.search(r"^\s*\|.*\|\s*$", c.get("content", ""), re.M):
                err("Text Display looks like it contains a markdown table; "
                    "Discord does not render those")

        elif t == CONTAINER:
            col = c.get("accent_color")
            if col is not None and not (isinstance(col, int) and 0 <= col <= 0xFFFFFF):
                err(f"accent_color {col!r} is not a 24-bit integer")

    return errs


def budget(payload):
    top = payload.get("components") or []
    every = list(walk(top))
    text = sum(len(c.get("content", "")) for c in every if c.get("type") == TEXT_DISPLAY)
    return len(top), len(every), text


# ---------------------------------------------------------------------------
# Markdown, the subset Discord actually renders in a Text Display
# ---------------------------------------------------------------------------


def _timestamp(m):
    """Render <t:UNIX:STYLE> the way a Discord client would."""
    when = datetime.datetime.fromtimestamp(int(m.group(1)), datetime.timezone.utc)
    style = m.group(2) or "f"
    if style == "R":
        secs = (when - datetime.datetime.now(datetime.timezone.utc)).total_seconds()
        ahead, secs = secs > 0, abs(secs)
        for unit, size in (("day", 86400), ("hour", 3600), ("minute", 60)):
            if secs >= size:
                n = int(secs // size)
                text = "%d %s%s" % (n, unit, "" if n == 1 else "s")
                return '<span class="ts-r">%s</span>' % (
                    ("in " + text) if ahead else (text + " ago"))
        return '<span class="ts-r">just now</span>'
    if style in ("d", "D"):
        return '<span class="ts-r">%s</span>' % when.strftime(
            "%d/%m/%Y" if style == "d" else "%d %B %Y")
    if style in ("t", "T"):
        return '<span class="ts-r">%s</span>' % when.strftime("%H:%M")
    return '<span class="ts-r">%s</span>' % when.strftime("%d %B %Y %H:%M")


def markdown(src):
    fences = []

    def stash(m):
        fences.append(m.group(1))
        return "\x00%d\x00" % (len(fences) - 1)

    out = re.sub(r"```(?:\w+)?\n(.*?)```", stash, src, flags=re.S)
    out = html.escape(out)
    out = re.sub(r"`([^`]+)`", r"<code>\1</code>", out)
    # Custom guild emoji. Discord writes them <:name:id>; the payloads carry
    # <:name:> because the id does not exist until the reconciler uploads it.
    out = re.sub(r"&lt;:(\w+):\d*&gt;",
                 lambda m: svg(m.group(1), size=18, cls="icon emoji"), out)
    out = re.sub(r"&lt;t:(\d+)(?::(\w))?&gt;", _timestamp, out)
    out = re.sub(r"\*\*(.+?)\*\*", r"<b>\1</b>", out)
    out = re.sub(r"(?<!\*)\*([^*]+)\*(?!\*)", r"<i>\1</i>", out)

    lines = []
    for line in out.split("\n"):
        head = re.match(r"^(#{1,3}) (.*)", line)
        if line.startswith("-# "):
            lines.append('<div class="subtext">%s</div>' % line[3:])
        elif head:
            lines.append('<div class="h h%d">%s</div>' % (len(head.group(1)), head.group(2)))
        elif line.startswith("&gt; "):
            lines.append('<div class="quote">%s</div>' % line[5:])
        elif line.strip() == "":
            lines.append('<div class="gap"></div>')
        else:
            lines.append("<div>%s</div>" % line)
    out = "".join(lines)

    for i, bodytext in enumerate(fences):
        out = out.replace("\x00%d\x00" % i, "<pre>%s</pre>" % html.escape(bodytext.rstrip()))
    return out


# ---------------------------------------------------------------------------
# Render. Discord's own dark theme, so what you judge is what players see.
# ---------------------------------------------------------------------------

CSS = """
""" + PALETTE + """
/* Discord's own palette, deliberately under its own names. It is a different
   system and the --dc- prefix is what stops the two being confused. */
:root{
 --dc-chat:#313338; --dc-panel:#2B2D31; --dc-fg:#DBDEE1; --dc-dim:#949BA4;
 --dc-rule:#3F4147; --dc-code:#1E1F22;
 --sans:"gg sans","Noto Sans","Noto Sans Fallback",Helvetica,Arial,sans-serif;
}
""" + BRAND_CSS + """
*{box-sizing:border-box}
html{background:var(--bg)}
body{margin:0;padding:2.5rem 1.5rem 4rem;background:var(--bg);color:var(--fg);
 font:15px/1.6 var(--mono);-webkit-font-smoothing:antialiased}
main{max-width:96rem;margin:0 auto}
h1{font-size:15px;font-weight:600;letter-spacing:.1em;text-transform:uppercase;margin:0}
.sub{color:var(--dim);margin:.35rem 0 0}
.hrule{border-top:1px solid var(--rule);margin:1.25rem 0 2rem}

.cols{display:flex;flex-wrap:wrap;gap:2rem;align-items:flex-start}
.col{flex:1 1 30rem;min-width:0}
.col>h2{font-size:15px;font-weight:600;margin:0 0 .2rem;color:var(--fg-hi);
 letter-spacing:.08em;text-transform:uppercase}
.col>.note{color:var(--dim);margin:0 0 .5rem;font-size:13px}
.chip{font-size:10px;letter-spacing:.08em;text-transform:uppercase;font-weight:600;
 border-radius:3px;padding:2px 6px;vertical-align:middle}
.chip.yes{background:var(--heart);color:#211F1B}
.chip.no{background:transparent;color:var(--dim);border:1px solid var(--rule)}
.meter{color:var(--dim);font-size:12px;margin:0 0 .9rem;line-height:1.7;
 font-variant-numeric:tabular-nums}
.meter b{color:var(--fg);font-weight:600}
.bad{color:var(--lose)}
.ok{color:var(--win)}
.errs{border:1px solid var(--lose);border-left-width:3px;padding:.6rem .8rem;
 margin:0 0 .9rem;color:var(--lose);font-size:13px}
.errs div+div{margin-top:.3rem}

/* The Discord frame. Everything below here is Discord's palette, not ours, so
   what gets judged is what a player sees rather than what our page prefers. */
.dc{background:var(--dc-chat);border-radius:8px;padding:1rem 1rem 1.2rem;
 font-family:var(--sans);font-size:15px;line-height:1.375;color:var(--dc-fg);
 overflow-x:auto}
.dc .who{display:flex;align-items:center;gap:.5rem;margin-bottom:.35rem}
.dc .av{width:26px;height:26px;border-radius:50%;background:var(--heart);flex:0 0 auto}
.dc .name{font-weight:600;color:#F2F3F5;font-size:14px}
.dc .tag{background:#5865F2;color:#fff;font-size:10px;font-weight:600;
 border-radius:3px;padding:1px 4px;letter-spacing:.02em}
.dc .ts{color:var(--dc-dim);font-size:12px}

.container{background:var(--dc-panel);border-radius:4px;border-left:4px solid var(--dc-rule);
 padding:.75rem 1rem;margin:.25rem 0}
.sep{border-top:1px solid var(--dc-rule);margin:.6rem 0}
.sep.pad{border-top:none;margin:.9rem 0}
.sep.lg{margin:1.1rem 0}

.h{font-weight:700;color:#F2F3F5;line-height:1.3}
.h1{font-size:24px;margin:.3rem 0 .1rem}
.h2{font-size:20px;margin:.3rem 0 .1rem}
.h3{font-size:16px;margin:.25rem 0 .1rem}
.subtext{font-size:12.5px;color:var(--dc-dim);line-height:1.4}
.quote{border-left:4px solid var(--dc-rule);padding-left:.6rem}
.gap{height:.55rem}
.icon{image-rendering:pixelated;display:block}
.emoji{display:inline-block;vertical-align:-.22em;margin:0 .08em}
/* Discord tints a rendered timestamp so it reads as generated, not typed. */
.ts-r{background:#3C4270;border-radius:3px;padding:0 2px}
code{background:var(--dc-code);border-radius:3px;padding:.1em .3em;
 font-family:var(--mono);font-size:12.5px}
pre{background:var(--dc-code);border:1px solid #2E3035;border-radius:4px;
 padding:.55rem .7rem;margin:.35rem 0;overflow-x:auto;
 font-family:var(--mono);font-size:12.5px;line-height:1.45;color:#C5C8CE}

.section{display:flex;gap:1rem;align-items:flex-start;margin:.35rem 0}
.section>.body{flex:1 1 auto;min-width:0}
.section>.acc{flex:0 0 auto}
.thumb{width:86px;height:86px;border-radius:4px;border:1px dashed var(--dc-rule);
 background:var(--dc-code);color:var(--dc-dim);font-size:10px;line-height:1.25;
 display:flex;align-items:center;justify-content:center;text-align:center;padding:.3rem;
 font-family:var(--mono)}
.gallery{margin:.5rem 0}
.gallery .shot.img{display:block;width:100%;height:auto;border:none;border-radius:4px;
 min-height:0;padding:0}
.gallery .shot{border:1px dashed var(--dc-rule);background:var(--dc-code);border-radius:4px;
 min-height:9rem;display:flex;align-items:center;justify-content:center;text-align:center;
 padding:1rem;color:var(--dc-dim);font-size:12px;font-family:var(--mono);line-height:1.5}
.row{display:flex;flex-wrap:wrap;gap:.5rem;margin:.6rem 0 .1rem}
.btn{background:#4E5058;color:#fff;border-radius:3px;padding:.42rem .75rem;
 font-size:14px;font-weight:500;display:inline-flex;align-items:center;gap:.35rem}
.btn .ext{color:#C4C9CE;font-size:11px}
footer{color:var(--dim);margin-top:2.5rem;font-size:13px}
"""


def inline_asset(url):
    """Return a data URI for url's basename if --assets holds a file by that name."""
    if not ASSETS or not url:
        return None
    f = ASSETS / Path(url).name
    if not f.is_file():
        return None
    mime = mimetypes.guess_type(f.name)[0] or "application/octet-stream"
    return "data:%s;base64,%s" % (mime, base64.b64encode(f.read_bytes()).decode())


def render(c):
    t = c.get("type")

    if t == CONTAINER:
        col = c.get("accent_color")
        style = ' style="border-left-color:#%06X"' % col if isinstance(col, int) else ""
        inner = "".join(render(k) for k in c.get("components") or [])
        return '<div class="container"%s>%s</div>' % (style, inner)

    if t == TEXT_DISPLAY:
        return '<div class="md">%s</div>' % markdown(c.get("content", ""))

    if t == SEPARATOR:
        cls = "sep" if c.get("divider", True) else "sep pad"
        if c.get("spacing") == 2:
            cls += " lg"
        return '<div class="%s"></div>' % cls

    if t == SECTION:
        body = "".join(render(k) for k in c.get("components") or [])
        acc = c.get("accessory") or {}
        return ('<div class="section"><div class="body">%s</div>'
                '<div class="acc">%s</div></div>' % (body, render(acc)))

    if t == THUMBNAIL:
        url = (c.get("media") or {}).get("url", "")
        return '<div class="thumb">image<br>%s</div>' % html.escape(Path(url).name or "?")

    if t == MEDIA_GALLERY:
        shots = ""
        for item in c.get("items") or []:
            url = (item.get("media") or {}).get("url", "")
            desc = item.get("description") or url
            data = inline_asset(url)
            if data:
                shots += '<img class="shot img" src="%s" alt="%s">' % (data, html.escape(desc))
            else:
                shots += '<div class="shot">%s</div>' % html.escape(desc)
        return '<div class="gallery">%s</div>' % shots

    if t == ACTION_ROW:
        return '<div class="row">%s</div>' % "".join(
            render(k) for k in c.get("components") or [])

    if t == BUTTON:
        ext = '<span class="ext">&#8599;</span>' if c.get("style") == 5 else ""
        return '<div class="btn">%s%s</div>' % (html.escape(c.get("label", "")), ext)

    if t == FILE:
        return '<div class="thumb">file</div>'

    return '<div class="md bad">unrenderable component type %s</div>' % html.escape(str(t))


def column(path, payload, errs):
    meta = payload.get("_meta") or {}
    top, total, chars = budget(payload)
    body = "".join(render(c) for c in payload.get("components") or [])

    status = meta.get("status", "")
    chosen = status.startswith("chosen")
    badge = ('<span class="chip %s">%s</span>' % (
        "yes" if chosen else "no", html.escape(status))) if status else ""

    box = ""
    if errs:
        box = '<div class="errs">%s</div>' % "".join(
            "<div>%s</div>" % html.escape(e) for e in errs)
    verdict = '<span class="bad">INVALID</span>' if errs else '<span class="ok">valid</span>'

    return """<div class="col">
<h2>%s &middot; %s %s</h2>
<p class="note">%s</p>
<p class="meter">%s &middot; top-level <b>%d</b>/%d &middot; total <b>%d</b>/%d
 &middot; text <b>%d</b>/%d<br><code>%s</code></p>
%s
<div class="dc">
 <div class="who"><div class="av"></div><span class="name">wly</span>
  <span class="tag">APP</span><span class="ts">today at 14:32</span></div>
 %s
</div>
</div>""" % (
        html.escape(meta.get("direction", "?").upper()),
        html.escape(meta.get("title") or path.stem), badge,
        html.escape(meta.get("note", "")),
        verdict, top, MAX_TOP_LEVEL, total, MAX_TOTAL, chars, MAX_TEXT_CHARS,
        html.escape(path.name), box, body)


def main():
    ap = argparse.ArgumentParser(description="Render Components V2 payload mockups.")
    ap.add_argument("--out", help="write the page here")
    ap.add_argument("--check", action="store_true", help="validate only, write nothing")
    ap.add_argument("--surface", default="*", help="glob over surface file names")
    ap.add_argument("--assets", default=None,
                    help="directory of images to inline, e.g. ../weloveyou-pack/assets")
    args = ap.parse_args()

    global ASSETS
    if args.assets:
        ASSETS = Path(args.assets)
        if not ASSETS.is_dir():
            print("::error::--assets %s is not a directory" % ASSETS, file=sys.stderr)
            return 1

    paths = sorted(SURFACES.glob("%s.json" % args.surface))
    if not paths:
        print("::error::no payloads matched %s in %s" % (args.surface, SURFACES),
              file=sys.stderr)
        return 1

    cols, failed = [], 0
    for p in paths:
        payload = json.loads(p.read_text(encoding="utf-8"))
        errs = validate(payload, p.name)
        if errs:
            failed += 1
            for e in errs:
                print("::error::%s" % e, file=sys.stderr)
        cols.append(column(p, payload, errs))

    if args.check or not args.out:
        print("%d/%d payloads valid" % (len(paths) - failed, len(paths)))
        return 1 if failed else 0

    # charset first, and within the first 1024 bytes, or a browser sniffs the
    # encoding and gets it wrong. Served over plain HTTP with no charset header
    # every heart, middot and arrow came out as mojibake, and nothing in the
    # source hinted at it. Publishing as an Artifact hid the bug, because that
    # wraps the file in its own head.
    page = """<meta charset="utf-8">
<title>%s</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Noto+Sans:wght@400;500;600;700&display=swap">
<style>%s</style>
<main>
%s
<p class="sub">The mockups are the payloads. Each column is the exact Components V2
JSON the bot will POST, rendered in Discord's own dark theme, with its budget
measured against the API's limits.</p>
<div class="hrule"></div>
<div class="cols">%s</div>
<footer>Rendered by <code>scripts/discord-mocks.py</code> from
<code>internal/discord/testdata/surfaces/</code>, with
<span class="hb">&#128150;</span>. Dashed boxes are assets that do not exist yet.</footer>
</main>""" % (title("Discord surfaces"), CSS,
             brandbar("Discord surfaces · design pass"),
             "".join(cols))

    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(page, encoding="utf-8", newline="\n")
    print("wrote %s  (%d payloads, %d invalid)" % (out, len(paths), failed))
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
