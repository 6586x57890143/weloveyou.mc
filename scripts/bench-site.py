#!/usr/bin/env python3
"""Render the benchmark JSON to a self-contained page.

    scripts/bench-site.py [--json BENCHMARKS.json]
                          [--screening BENCHMARKS-screening.json] [--out site]

Reads the machine-readable twin that `wly bench` writes rather than parsing the
Markdown back: the numbers are published after a human has checked and merged
them, by which point the run is long gone, and re-parsing our own prose would be
fragile in the one place fragility is least visible.

Both files render when both exist, confirmed first, screening under its own
banner. A screening pass is a single run with no variance, so the page says so
loudly rather than trusting the reader to remember which file a row came from.

Stdlib only, and one file out with no external assets. The point is to publish
numbers, not to acquire a site generator.
"""
import argparse
import html
import json
import pathlib
import sys

# One glyph means "no value", everywhere. There used to be three: a literal
# ", " left behind when commit a08b9f8 swapped em dashes for plain punctuation
# and caught these placeholders, a "-" for chunk counts on tick workloads, and
# a bare empty cell. Three spellings of nothing read as three different things.
EMPTY = "-"

# Accent colours are Minecraft's own chat palette, desaturated for reading on a
# dark background. Written down so the next page in this project can match it
# rather than guess:
#
#   §a #55FF55 -> --win   better than baseline
#   §c #FF5555 -> --lose  worse than baseline, and failures
#   §6 #FFAA00 -> --base  the baseline row, warnings
#   §b #55FFFF -> --info  links
#   §7 #AAAAAA -> --dim   secondary text
#
# Box-drawing glyphs are used where monospace alignment is safe and they carry
# meaning: the header frame, section rules, the bars, the row markers. Table
# grid lines are CSS borders, NOT drawn characters. A real +--+--+ grid breaks
# the moment a cell wraps or the viewport narrows, and a table that only lines
# up at one width is worse than one that never pretended to.
CSS = """
:root{
 --bg:#211F1B; --panel:#282621; --rule:#3B372F; --rule-hi:#564E42;
 --fg:#D5CEC1; --fg-hi:#EFE9DC; --dim:#8E8677;
 --win:#8FA860; --lose:#C4705C; --base:#D8A657; --info:#84A69C;
 --heart:#E39AAE;
 --mono:ui-monospace,"Cascadia Code","JetBrains Mono",Menlo,Consolas,"DejaVu Sans Mono",monospace;
}
*{box-sizing:border-box}
html{background:var(--bg)}
body{margin:0;padding:2.5rem 1.5rem 4rem;background:var(--bg);color:var(--fg);
 font:15px/1.6 var(--mono);font-variant-numeric:tabular-nums;
 -webkit-font-smoothing:antialiased}
main{max-width:76rem;margin:0 auto}
/* Two columns across the top: what this is on the left, what it found on the
   right. Keeps line length readable without leaving half the width empty. */
.head{margin:1.25rem 0 0}
.head>.left{flex:0 1 42%;min-width:26ch}
.head>.right{flex:1 1 55%;min-width:0}
/* Notes sit side by side above their table, so they fill the same width. */
.notes{display:flex;flex-wrap:wrap;gap:.4rem 2.5rem;margin:.5rem 0 .9rem}
.notes>.note{flex:1 1 30ch;margin:0;max-width:none}
a{color:var(--info);text-decoration:none;border-bottom:1px solid transparent}
a:hover{border-bottom-color:var(--info)}
code{font-family:var(--mono)}

/* Frame. The corner glyphs are real characters; the run between them is a
   repeated dash clipped by overflow, so it fits any width exactly. */
.hrule{border-top:1px solid var(--rule-hi)}
.bar-row{display:flex;align-items:baseline;gap:1ch;padding-bottom:.5rem}
.bar-row .t{flex:1 1 auto;display:flex;align-items:baseline;gap:.9ch;flex-wrap:wrap}
.bar-row .d{color:var(--dim);letter-spacing:.04em}
/* The server is called weloveyou. The heart belongs on the page, not only in
   the browser tab, so the name carries it and the pink is a real token. */
.brand{color:var(--heart);font-size:19px;font-weight:600;letter-spacing:.02em}
.brand .hb{font-size:16px}
.wordmark{color:var(--dim);letter-spacing:.14em;text-transform:uppercase;font-size:13px}
footer .hb{color:var(--heart)}

h2{font-size:15px;font-weight:600;margin:2.25rem 0 .4rem;color:var(--fg-hi);
 letter-spacing:.1em;text-transform:uppercase}
.sub{color:var(--dim);margin:0}

.census b{color:var(--fg-hi);font-weight:600}
.note{color:var(--dim);margin:.5rem 0 1rem;max-width:none;white-space:normal}
.note strong{color:var(--fg);font-weight:600}

/* Dotted leader lines. */
.leads{margin:0;columns:2;column-gap:3rem}
.lead{break-inside:avoid}
.lead{display:flex;align-items:baseline;gap:.75ch;min-width:0}
.lead .k{white-space:nowrap;color:var(--dim);letter-spacing:.06em;flex:0 0 auto}
.lead .d{flex:1 1 auto;min-width:1.5ch;border-bottom:1px dotted var(--rule);
 transform:translateY(-.3em)}
.lead .v{flex:0 1 auto;min-width:0;color:var(--fg-hi);overflow:hidden;text-overflow:ellipsis;
 white-space:nowrap}
.lead .v .q{color:var(--dim)}

.wrap{overflow-x:auto;border:1px solid var(--rule);background:var(--panel)}
table{border-collapse:collapse;width:100%;white-space:nowrap;font-size:14px}
th,td{padding:.5rem .75rem;text-align:right;border-bottom:1px solid var(--rule)}
th:nth-child(3),td:nth-child(3){text-align:left}
thead th{color:var(--dim);font-weight:500;letter-spacing:.08em;font-size:12px;
 text-transform:uppercase;background:var(--bg);padding-top:.75rem;padding-bottom:.75rem}
tbody tr:last-child td{border-bottom:0}
tbody tr:hover td{background:#2E2B25}
td.rank{color:var(--rule-hi);padding-right:.2rem}
tr.is-failed td.rank{color:var(--lose)}
td.name{color:var(--fg-hi)}
tr.is-base td{background:#2B2822}
tr.is-base td.name,tr.is-base td.rank,td.mark{color:var(--base)}
td.mark{padding:0 0 0 .35rem;width:1ch}
td.bar{color:var(--rule-hi);letter-spacing:.12em;padding-left:.15rem;text-align:left;opacity:.85}
td.bar.win{color:var(--win)}
td.bar.lose{color:var(--lose)}
.win{color:var(--win)}
.lose{color:var(--lose)}
.dim{color:#A29788}
td.failed{color:var(--lose);letter-spacing:.14em;text-align:left}

.banner{border:1px solid var(--rule);border-left:3px solid var(--base);
 background:var(--panel);padding:1rem 1.25rem;margin:3rem 0 .5rem;color:var(--dim);
 white-space:normal;font-size:14px;line-height:1.6;margin-top:2rem}
.banner+h2{margin-top:1.75rem}
.banner b{color:var(--base);font-weight:600;letter-spacing:.1em;font-size:13px}
.banner strong{color:var(--fg)}
.caveats{border:1px solid var(--rule);background:var(--panel);
 padding:.9rem 1.2rem;margin:2.5rem 0 0}
.caveats h3{margin:0 0 1rem;font-size:12px;color:var(--dim);font-weight:500;
 letter-spacing:.1em;text-transform:uppercase}
.caveats ul{margin:0;padding-left:1.1rem;columns:2;column-gap:2.5rem}
.caveats li{break-inside:avoid}
.caveats li{margin:0 0 .5rem;color:var(--dim);font-size:13px;line-height:1.55}
.caveats strong{color:var(--fg)}
footer{margin-top:2.5rem;padding-top:1rem;border-top:1px solid var(--rule);
 color:var(--dim);text-align:center}
/* Two columns of leaders only when there is room for the values; below
   this they were being cut off with an ellipsis, which loses the number. */
@media(max-width:1000px){.leads{columns:1}}

/* Below this the table stops being a table. Fourteen columns need about a
   thousand pixels; under that, reading one row means scrolling sideways and
   losing the profile name off the left edge.

   The replacement is still a table, just wrapped: name and rank on a heading
   line, then every value on a dense grid with its label inline. Stacking one
   label/value pair per line instead would be the same data four times as tall
   with nothing to catch the eye. */
@media(max-width:1000px){
 .wrap{overflow-x:visible;border:0;background:transparent}
 thead{display:none}
 tbody,td{display:block;width:100%}
 tr{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:0 1.25rem;
  border:1px solid var(--rule);background:var(--panel);margin:0 0 .4rem;
  padding:.5rem .75rem;line-height:1.45}
 tr.is-base{border-color:var(--base)}
 tr.is-failed{border-color:var(--lose)}
 tbody tr:hover td{background:transparent}
 td{display:flex;gap:.6ch;align-items:baseline;border:0;padding:.05rem 0;text-align:left}
 td::before{content:attr(data-label);color:var(--dim);font-size:11px;
  letter-spacing:.06em;text-transform:uppercase;flex:0 0 auto}
 /* Rank and name are the heading, so they span the card and drop their labels. */
 td.rank,td.name{grid-column:1/-1;padding:0}
 td.rank{color:var(--dim);font-size:11px}
 td.name{font-size:15px;margin-bottom:.35rem}
 td.rank::before,td.name::before{content:none}
 td.primary{color:var(--fg-hi);font-weight:600}
 /* Both redundant here: the border says which row is the baseline, and the
    rank already carries the ordering. */
 td.mark,td.bar{display:none}
 td.failed{grid-column:1/-1}
 td.failed::before{content:none}
}
@media(max-width:620px){tr{grid-template-columns:repeat(2,minmax(0,1fr))}}
"""

# Three, not nine. The page is a results table; anything that has to be said
# about a specific number is said next to that number instead.
CAVEATS = [
    "Two cores, aarch64, an Oracle Ampere A1. The same job runs roughly ten times "
    "faster on a desktop, so these numbers only compare against each other.",
    "<strong>Peak CPU is per core</strong>, so 200% is both cores saturated. Well "
    "under that means a profile left parallelism on the table.",
    "<code>~</code> is inside the noise floor. <strong>FAILED</strong> means no run "
    "finished, which is a gap rather than a score of zero.",
]

BASELINE = "baseline-j21"
BAR_CELLS = 12
# Eighth blocks, so a bar can end between two character cells and still line up.
EIGHTHS = "▏▎▍▌▋▊▉█"
FULL = "█"


def bar(frac, cells=BAR_CELLS):
    """An ASCII bar. Longer is always better, whichever way the metric reads."""
    frac = max(0.0, min(1.0, frac))
    total = frac * cells
    full = int(total)
    out = FULL * full
    rem = total - full
    if full < cells and rem > 0.06:
        out += EIGHTHS[min(7, int(rem * 8))]
    return out or EIGHTHS[0]


def delta_class(text):
    """Colour by the sign of vs-baseline. `~` is inside the noise floor."""
    t = (text or "").strip()
    if t.startswith("+"):
        return "win"
    if t.startswith("-") and t != EMPTY:
        return "lose"
    return "dim"


def cell(value, cls="", label="", extra=""):
    """One table cell. The label rides along as data-label so the narrow layout
    can print it beside the value once the table stops being a table."""
    attrs = f' class="{cls}"' if cls else ""
    if label:
        attrs += f' data-label="{html.escape(label, quote=True)}"'
    return f"<td{attrs}{extra}>{value}</td>"


def num(value, fmt, suffix=""):
    """Format a number, or the one empty glyph when there isn't one."""
    return format(value, fmt) + suffix if value else EMPTY


def human_bytes(n):
    if not n:
        return EMPTY
    for unit in ("B", "KiB", "MiB", "GiB", "TiB"):
        if n < 1024 or unit == "TiB":
            return f"{n:.0f} {unit}" if unit in ("B", "KiB") else f"{n:.2f} {unit}"
        n /= 1024
    return EMPTY


def hardware_of(doc):
    """The distinct machines these rows were measured on."""
    seen, out = set(), []
    for wl in (doc.get("workloads") or {}).values():
        for row in wl.get("rows") or []:
            hw = row.get("hardware") or {}
            key = (hw.get("model"), hw.get("cpus"), hw.get("threads_per_core"))
            if not hw.get("model") or key in seen:
                continue
            seen.add(key)
            desc = f"{hw['model']} · {hw.get('cpus', '?')} vCPU"
            if (hw.get("threads_per_core") or 1) > 1:
                desc += f" ({hw['threads_per_core']} threads/core)"
            if hw.get("memory_mb"):
                desc += f" · {hw['memory_mb'] / 1024:.0f} GB"
            out.append(desc + f" · {hw.get('arch', '?')}")
    return out


def lead(key, value):
    """One dotted-leader line. The dots are clipped by overflow, so any width fits."""
    return (
        '<div class="lead">'
        f'<span class="k">{html.escape(key)}</span>'
        '<span class="d"></span>'
        f'<span class="v">{value}</span></div>'
    )


def frame(title, subtitle, right):
    """The header block: the name and its heart, then what the page is."""
    return (
        '<div class="bar-row"><span class="t">'
        f'<span class="brand">{html.escape(title)} <span class="hb">💖</span></span>'
        f'<span class="wordmark">{html.escape(subtitle)}</span></span>'
        f'<span class="d">{html.escape(right)}</span></div>'
        '<div class="hrule"></div>' 
    )


def summary(doc, parts):
    """The run, as data. This is a results page: what someone needs from the
    top of it is what was measured, on what, from which commit and with which
    settings, not a paragraph about why benchmarks are hard."""
    workloads = doc.get("workloads") or {}
    rows = [r for wl in workloads.values() for r in (wl.get("rows") or [])]
    failed = sorted({r.get("profile", "?") for r in rows if r.get("failed")})
    reps = doc.get("repeats_per_profile") or 0

    lines = []
    if gen := doc.get("generated"):
        lines.append(("generated", html.escape(gen.replace("T", " ").rstrip("Z") + " UTC")))
    if commit := doc.get("commit"):
        lines.append(("commit", f"<code>{html.escape(commit[:12])}</code>"))
    for hw in hardware_of(doc)[:1]:
        lines.append(("hardware", html.escape(hw)))
    if len(hardware_of(doc)) > 1:
        lines.append(("hardware", '<span class="lose">mixed, not comparable</span>'))
    if (host := doc.get("host")) and host != "unrecorded":
        lines.append(("boxes", f"<code>{html.escape(host)}</code>"))
    lines.append(("profiles", f"{len(rows)} rows over {len(workloads)} workloads"))
    lines.append(("runs each", str(reps)))
    if seed := doc.get("seed"):
        lines.append(("seed", f"<code>{html.escape(seed)}</code>"))
    if radius := doc.get("radius"):
        lines.append(("pregen radius", f"{radius} blocks"))
    if load := doc.get("load"):
        lines.append(("tick load", f"{load:g}x"))
    for key in doc.get("workload_order") or list(workloads):
        wl = workloads.get(key)
        if wl and wl.get("mods_loaded"):
            lines.append((f"mods, {key}", str(wl["mods_loaded"])))
    for key in doc.get("workload_order") or list(workloads):
        wl = workloads.get(key)
        live = [r for r in ((wl or {}).get("rows") or []) if not r.get("failed")]
        if not live:
            continue
        best = (min if wl.get("primary_lower_is_better") else max)(
            live, key=lambda r: r.get("primary_value") or 0
        )
        lines.append((
            f"best, {key}",
            f"<code>{html.escape(best.get('profile', '?'))}</code>"
            f'<span class="q"> {best.get("primary_value") or 0:.1f} '
            f'{html.escape(wl.get("primary_metric") or "")}</span>',
        ))
    if failed:
        lines.append(("failed", '<span class="lose">'
                      + ", ".join(html.escape(f) for f in failed) + "</span>"))
    lines.append(("noise floor", f"{(doc.get('noise_floor') or 0.05) * 100:.0f}%"))

    parts.append('<div class="leads">')
    parts += [lead(k, v) for k, v in lines]
    parts.append("</div>")


def workload_table(key, wl, parts):
    metric = wl.get("primary_metric") or "chunks/s"
    lower = bool(wl.get("primary_lower_is_better"))
    worldgen = wl.get("worldgen", True)

    parts.append(f"<h2>{html.escape(wl.get('title', key))}</h2>")

    # Collected and emitted as one row rather than as a stack of short
    # paragraphs, so they fill the width the table already occupies.
    notes = [
        f'<p class="note">Ranked by <strong>{html.escape(metric)}</strong>, '
        f'{"lower" if lower else "higher"} is better. <code>vs base</code> is signed so '
        "a plus is better.</p>"
    ]
    if worldgen:
        # Measured, not asserted: on the first real sweep every profile reported
        # TPS 20.0 and MSPT p95 between 0.5ms and 0.9ms on workload B.
        notes.append(
            '<p class="note">MSPT and TPS sit at idle here. Pregeneration barely ticks '
            "entities; only the tick workloads move them.</p>"
        )
    if key == "machines":
        notes.append(
            '<p class="note">Machines are powered but unfed: this is tick and energy '
            "cost, not throughput.</p>"
        )
    parts.append('<div class="notes">' + "".join(notes) + "</div>")

    rows = list(wl.get("rows") or [])
    live = [r for r in rows if not r.get("failed")]
    # Best first. The committed Markdown sorts baseline-then-alphabetical, which
    # is right for reading a diff and wrong for reading a ranking.
    live.sort(key=lambda r: r.get("primary_value") or 0, reverse=not lower)
    span = max((r.get("primary_value") or 0) for r in live) if live else 0
    floor = min((r.get("primary_value") or 0) for r in live) if live else 0

    # Column headings and row cells come from one list, so a column cannot
    # drift away from its heading, and every cell carries its own label. The
    # labels are what the narrow layout prints beside each value once the table
    # stops being a table.
    heads = [
        "#", "", "profile", "heap", metric, "", "vs base",
        # MSPT median beside whichever percentile the workload is read by. On a
        # tick workload the primary column is already p95, so repeating it here
        # was one column saying the same thing twice; the median is the half
        # that says whether a collector stalls routinely or only rarely, and it
        # was in the JSON but displayed nowhere.
        "MSPT med", "TPS", "RSS", "CPU", "boot", "GC95", "GC99",
    ]
    parts.append(
        '<div class="wrap"><table><thead><tr>'
        + "".join(f"<th>{html.escape(h)}</th>" for h in heads)
        + "</tr></thead><tbody>"
    )
    for i, row in enumerate(live + [r for r in rows if r.get("failed")], 1):
        is_base = row.get("profile") == BASELINE
        failed = bool(row.get("failed"))
        cells = [
            ("✕" if failed else f"{i:02d}", "rank"),
            ("◆" if is_base else "", "mark"),
            (f"<code>{html.escape(row.get('profile', '?'))}</code>", "name"),
            (html.escape(row.get("heap") or EMPTY), ""),
        ]
        if failed:
            # An absence, not a score. Every metric would otherwise read zero,
            # and the delta against a working baseline a confident -100%.
            cells.append(("FAILED", "failed"))
        else:
            value = row.get("primary_value") or 0
            # Proportional to the value, from zero, so the length can be read as
            # the number itself. Normalising across the observed range instead
            # stretched a fifth of a difference over the full width, and drew
            # the slowest row as a one-cell sliver that looked like a fault.
            if lower:
                frac = (floor / value) if value else 0.0
            else:
                frac = (value / span) if span else 0.0
            cls = delta_class(row.get("vs_baseline"))
            cells += [
                (f"{value:.1f}", "primary"),
                (bar(frac), f"bar {cls}"),
                (html.escape(row.get("vs_baseline") or EMPTY), cls),
                (num(row.get("mspt_median_ms"), ".1f", "ms"), ""),
                (num(row.get("tps"), ".1f"), ""),
                (human_bytes(row.get("peak_rss_bytes")), ""),
                (num(row.get("peak_cpu_percent"), ".0f", "%"), ""),
                (num(row.get("startup_sec"), ".1f", "s"), ""),
                (num(row.get("gc_pause_p95_ms"), ".0f", "ms"), ""),
                (num(row.get("gc_pause_p99_ms"), ".0f", "ms"), ""),
            ]
        opener = "<tr>"
        if is_base:
            opener = '<tr class="is-base">'
        elif failed:
            opener = '<tr class="is-failed">'
        out = [opener]
        for (val, cls_), label in zip(cells, heads):
            extra = ' colspan="10"' if cls_ == "failed" else ""
            out.append(cell(val, cls_, label, extra))
        parts.append("".join(out) + "</tr>")
    parts.append("</tbody></table></div>")

    watch = sorted({r.get("profile", "?") for r in rows if r.get("watchdog")})
    if watch:
        parts.append(
            '<p class="note"><span class="lose">The watchdog killed the server on '
            + ", ".join(f"<code>{html.escape(p)}</code>" for p in watch)
            + ".</span> A hung tick is a failed profile, not a slow one.</p>"
        )
    dropped = sorted({f for r in rows for f in (r.get("dropped_flags") or [])})
    if dropped:
        parts.append(
            '<p class="note">Refused by the JVM and dropped before the run: '
            + ", ".join(f"<code>{html.escape(f)}</code>" for f in dropped)
            + ".</p>"
        )


def sections(doc, parts):
    workloads = doc.get("workloads") or {}
    # Order comes from the JSON. A JSON object is unordered and Go marshals map
    # keys alphabetically, so without workload_order the site listed workload E
    # before workload B and the control stopped being the first thing anyone read.
    for key in doc.get("workload_order") or list(workloads):
        if workloads.get(key):
            workload_table(key, workloads[key], parts)


def render(doc, screening=None):
    doc, screening = doc or {}, screening or {}
    has_confirmed = bool(doc.get("workloads"))
    has_screening = bool(screening.get("workloads"))
    primary = doc if has_confirmed else screening

    parts = ["<main>"]
    parts.append(
        frame(
            "weloveyou",
            "JVM benchmarks",
            (primary.get("generated") or "")[:10] or "no data",
        )
    )

    if not (has_confirmed or has_screening):
        # An honest empty state beats a broken build. pages.yml also triggers
        # when its own files change, so the first run happens before any sweep
        # has merged.
        parts.append(
            '<div class="banner"><b>NO RESULTS YET</b><br>The sweep runs on a two-core '
            "Ampere A1 that sits powered off unless it is measuring, so numbers arrive "
            "in batches. Nothing goes up until someone has read it and merged it.</div>"
        )
    else:
        parts.append('<div class="head">')
        summary(primary, parts)
        parts.append("</div>")

    if has_confirmed:
        sections(doc, parts)
    if has_screening:
        parts.append(
            '<div class="banner"><b>SCREENING PASS</b><br>One run each, so there is no '
            "variance here and one noisy neighbour is enough to move a row. This pass "
            "picks what is worth measuring properly. <strong>Do not read it next to "
            "confirmed numbers.</strong></div>"
        )
        sections(screening, parts)

    parts.append('<div class="caveats"><h3>How to read this</h3><ul>')
    parts += [f"<li>{c}</li>" for c in CAVEATS]
    parts.append("</ul></div>")
    parts.append(
        '<footer>Measured with <code>wly bench</code> on one very small computer, '
        'with <span class="hb">💖</span>, from '
        '<a href="https://github.com/6586x57890143/weloveyou.mc">weloveyou.mc</a>.<br>'
        "Nothing here is believed until it has a row in this table.</footer>"
    )
    parts.append("</main>")

    body = "\n".join(parts)
    return (
        "<!doctype html>\n"
        '<html lang="en"><head><meta charset="utf-8">'
        '<meta name="viewport" content="width=device-width,initial-scale=1">'
        '<meta name="color-scheme" content="dark">'
        "<title>weloveyou \U0001f496 JVM benchmarks</title>"
        f"<style>{CSS}</style></head><body>\n{body}\n</body></html>\n"
    )


def load(path):
    src = pathlib.Path(path)
    if not src.exists():
        # Not an error. pages.yml also triggers when its own files change, so the
        # very first run happens before any sweep has been merged, and failing
        # there makes a perfectly good merge look broken.
        print(f"::notice::{src} does not exist yet")
        return {}
    return json.loads(src.read_text(encoding="utf-8")) or {}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--json", default="BENCHMARKS.json")
    ap.add_argument("--screening", default="BENCHMARKS-screening.json")
    ap.add_argument("--out", default="site")
    args = ap.parse_args()

    doc, screening = load(args.json), load(args.screening)

    outdir = pathlib.Path(args.out)
    outdir.mkdir(parents=True, exist_ok=True)
    (outdir / "index.html").write_text(
        render(doc, screening), encoding="utf-8", newline="\n"
    )
    # The data alongside the page: anyone comparing our numbers to their own
    # should not have to scrape a table to do it.
    for name, payload in (("benchmarks.json", doc), ("screening.json", screening)):
        if payload:
            (outdir / name).write_text(
                json.dumps(payload, indent=2) + "\n", encoding="utf-8", newline="\n"
            )
    print(f"wrote {outdir / 'index.html'}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
