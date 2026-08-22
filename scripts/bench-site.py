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
 --bg:#0C0E10; --panel:#121519; --rule:#232A31; --rule-hi:#33404B;
 --fg:#C3CBD4; --fg-hi:#E8EEF4; --dim:#6E7A87;
 --win:#62E063; --lose:#F0554F; --base:#E8A33D; --info:#57D9D9;
 --mono:ui-monospace,"Cascadia Code","JetBrains Mono",Menlo,Consolas,"DejaVu Sans Mono",monospace;
}
*{box-sizing:border-box}
html{background:var(--bg)}
body{margin:0;padding:3rem 1.25rem 5rem;background:var(--bg);color:var(--fg);
 font:13px/1.55 var(--mono);font-variant-numeric:tabular-nums;
 -webkit-font-smoothing:antialiased}
main{max-width:68rem;margin:0 auto}
.prose{max-width:44rem}
a{color:var(--info);text-decoration:none;border-bottom:1px solid transparent}
a:hover{border-bottom-color:var(--info)}
code{font-family:var(--mono)}

/* Frame. The corner glyphs are real characters; the run between them is a
   repeated dash clipped by overflow, so it fits any width exactly. */
.hrule{display:flex;color:var(--rule-hi);user-select:none;line-height:1}
.hrule i{flex:1 1 auto;overflow:hidden;white-space:nowrap;font-style:normal}
.bar-row{display:flex;align-items:baseline;gap:1ch;color:var(--rule-hi);line-height:1.9}
.bar-row .edge{user-select:none}
.bar-row .t{flex:1 1 auto;color:var(--fg-hi);letter-spacing:.16em;font-weight:600}
.bar-row .d{color:var(--dim);letter-spacing:.04em}

h2{font-size:13px;font-weight:600;margin:3.5rem 0 .25rem;color:var(--fg-hi);
 letter-spacing:.14em;text-transform:uppercase}
.sub{color:var(--dim);margin:.9rem 0 0}
.census{color:var(--dim);margin:.75rem 0 0;letter-spacing:.02em}
.census b{color:var(--fg-hi);font-weight:600}
.note{color:var(--dim);margin:.3rem 0 .9rem;max-width:52rem;white-space:normal}
.note strong{color:var(--fg);font-weight:600}

/* Dotted leader lines. */
.leads{margin:1.75rem 0 2.5rem}
.lead{display:flex;align-items:baseline;gap:.75ch}
.lead .k{white-space:nowrap;color:var(--dim);letter-spacing:.08em}
.lead .d{flex:1 1 auto;min-width:2ch;overflow:hidden;white-space:nowrap;color:var(--rule)}
.lead .v{white-space:nowrap;color:var(--fg-hi)}
.lead .v .q{color:var(--dim)}

.wrap{overflow-x:auto;border:1px solid var(--rule);background:var(--panel)}
table{border-collapse:collapse;width:100%;white-space:nowrap}
th,td{padding:.34rem .7rem;text-align:right;border-bottom:1px solid var(--rule)}
th:nth-child(3),td:nth-child(3){text-align:left}
thead th{color:var(--dim);font-weight:400;letter-spacing:.1em;font-size:11px;
 text-transform:uppercase;background:var(--bg)}
tbody tr:last-child td{border-bottom:0}
tbody tr:hover td{background:#171C21}
td.rank{color:var(--rule-hi);padding-right:.2rem}
td.name{color:var(--fg-hi)}
tr.is-base td{background:#16161A}
tr.is-base td.name,tr.is-base td.rank,td.mark{color:var(--base)}
td.mark{padding:0 0 0 .35rem;width:1ch}
td.bar{color:var(--rule-hi);letter-spacing:-.08em;padding-left:.15rem;text-align:left}
td.bar.win{color:var(--win)}
td.bar.lose{color:var(--lose)}
.win{color:var(--win)}
.lose{color:var(--lose)}
.dim{color:var(--dim)}
td.failed{color:var(--lose);letter-spacing:.14em;text-align:left}

.banner{border:1px solid var(--rule);border-left:2px solid var(--base);
 background:var(--panel);padding:.75rem 1rem;margin:3.5rem 0 0;color:var(--dim);
 max-width:52rem;white-space:normal}
.banner b{color:var(--base);font-weight:600;letter-spacing:.12em}
.banner strong{color:var(--fg)}
.caveats{border:1px solid var(--rule);background:var(--panel);
 padding:1.1rem 1.3rem;margin:3.5rem 0 0;max-width:52rem}
.caveats h3{margin:0 0 .7rem;font-size:11px;color:var(--dim);font-weight:400;
 letter-spacing:.14em;text-transform:uppercase}
.caveats ul{margin:0;padding-left:1.1rem}
.caveats li{margin:.45rem 0;color:var(--dim)}
.caveats strong{color:var(--fg)}
footer{margin-top:3.5rem;padding-top:1rem;border-top:1px solid var(--rule);color:var(--dim)}
@media(max-width:640px){body{padding:1.5rem .75rem 3rem}th,td{padding:.3rem .5rem}}
"""

CAVEATS = [
    "Measured on the box the server actually runs on, an Oracle Ampere A1, "
    "<strong>2 cores, aarch64</strong>. Numbers from a desktop would be a different "
    "machine's answer: the same job runs about ten times faster on a Ryzen 9600X.",
    "A <strong>screening</strong> pass is a single run with no variance to report. "
    "A <strong>confirmed</strong> pass is the median of repeats. Never compare one "
    "against the other.",
    "Workload B measures the pack as it was on the day. <strong>The pack grows</strong>, "
    "so the mod count is printed with every table, rows taken against different packs "
    "are not comparable.",
    "Anything inside the noise floor is shown as <code>~</code> rather than a number, "
    "because on a shared VM it is not a result.",
    "<strong>Peak CPU is relative to one core</strong>, so 200% means both cores were "
    "saturated. A profile well under that left parallelism unused, often the "
    "explanation for a throughput number that looks disappointing.",
    "<strong>Core count is part of the result, not a footnote.</strong> These run on "
    "two cores. Mods that parallelise the tick loop can be a clear win on a "
    "twelve-thread desktop and deadlock here, which is not a tuning difference, it "
    "is a different machine.",
    "<strong>MSPT is the number players feel.</strong> A tick is 50ms; a 95th "
    "percentile above that means the server is routinely behind, however good its "
    "chunk throughput looks. Measured with spark, whose background profiler is "
    "disabled, it segfaults the JVM on Java 25/aarch64.",
    "<strong>Heap is a dimension, not a constant.</strong> The useful question is not "
    "whether the server can use the whole box, it is where more memory stops buying "
    "throughput.",
    "<strong>A row marked FAILED produced no run at all.</strong> It is an absence, "
    "not a score of zero.",
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


def cell(value, cls=""):
    return f'<td class="{cls}">{value}</td>' if cls else f"<td>{value}</td>"


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
        f'<span class="d">{"·" * 260}</span>'
        f'<span class="v">{value}</span></div>'
    )


def frame(title, right):
    """The header block. Corner glyphs are real; the runs between them scale."""
    return (
        '<div class="hrule">┌<i>' + "─" * 260 + "</i>┐</div>"
        '<div class="bar-row"><span class="edge">│</span>'
        f'<span class="t">{html.escape(title)}</span>'
        f'<span class="d">{html.escape(right)}</span>'
        '<span class="edge">│</span></div>'
        '<div class="hrule">╞<i>' + "═" * 260 + "</i>╡</div>"
    )


def summary(doc, parts):
    """The lines that answer the question someone arrived with, above the tables."""
    hw = hardware_of(doc)
    parts.append('<div class="leads">')
    if len(hw) == 1:
        parts.append(lead("measured on", html.escape(hw[0])))
    elif len(hw) > 1:
        parts.append(
            lead(
                "measured on",
                '<span class="lose">'
                + html.escape(" / ".join(hw))
                + ", mixed and not comparable</span>",
            )
        )
    # Only when it is actually known. Shards written before Result.Host existed
    # render as "unrecorded", and the hardware line above already carries the
    # provenance that matters; a leader line saying nothing is just noise.
    if (host := doc.get("host")) and host != "unrecorded":
        parts.append(lead("boxes", f"<code>{html.escape(host)}</code>"))

    workloads = doc.get("workloads") or {}
    for key in doc.get("workload_order") or list(workloads):
        wl = workloads.get(key)
        if not wl:
            continue
        live = [r for r in (wl.get("rows") or []) if not r.get("failed")]
        if not live:
            continue
        best = (min if wl.get("primary_lower_is_better") else max)(
            live, key=lambda r: r.get("primary_value") or 0
        )
        metric = wl.get("primary_metric") or "chunks/s"
        parts.append(
            lead(
                f"fastest, {key}",
                f"<code>{html.escape(best.get('profile', '?'))}</code>"
                f'  <span class="q">{best.get("primary_value") or 0:.1f} '
                f"{html.escape(metric)}</span>",
            )
        )

    failed = sorted(
        {
            r.get("profile", "?")
            for wl in workloads.values()
            for r in (wl.get("rows") or [])
            if r.get("failed")
        }
    )
    if failed:
        parts.append(
            lead(
                "profiles that failed",
                '<span class="lose">'
                + ", ".join(html.escape(p) for p in failed)
                + "</span>",
            )
        )
    parts.append(lead("noise floor", f"{(doc.get('noise_floor') or 0.05) * 100:.0f}%"))
    parts.append("</div>")


def workload_table(key, wl, parts):
    metric = wl.get("primary_metric") or "chunks/s"
    lower = bool(wl.get("primary_lower_is_better"))
    worldgen = wl.get("worldgen", True)

    parts.append(f"<h2>{html.escape(wl.get('title', key))}</h2>")
    parts.append(
        f'<p class="note">Read by <strong>{html.escape(metric)}</strong>, '
        f'{"lower" if lower else "higher"} is better. <code>vs base</code> compares it, '
        "signed so a positive number is always the better one.</p>"
    )
    if worldgen:
        # Measured, not guessed: on the first real sweep every profile reported
        # TPS 20.0 and MSPT p95 between 0.5ms and 0.9ms on workload B. Publishing
        # fifty rows of that without saying so presents noise as a result.
        parts.append(
            '<p class="note">MSPT and TPS are <strong>not meaningful here</strong>. '
            "Pregeneration barely ticks entities, so they sit at idle whatever the flags "
            "do; the tick workloads are what move them.</p>"
        )
    if key == "machines":
        parts.append(
            '<p class="note">Machines are powered but not fed: this is block entity '
            "ticking and energy network cost, not production throughput.</p>"
        )
    if wl.get("mods_loaded"):
        tail = (
            " (Fabric API and the pregenerator).</p>"
            if worldgen and key == "vanilla"
            else ". Rows are only comparable to runs against a similar pack, and this "
            "pack grows.</p>"
        )
        parts.append(f'<p class="note">{wl["mods_loaded"]} mods loaded' + tail)

    rows = list(wl.get("rows") or [])
    live = [r for r in rows if not r.get("failed")]
    # Best first. The committed Markdown sorts baseline-then-alphabetical, which
    # is right for reading a diff and wrong for reading a ranking.
    live.sort(key=lambda r: r.get("primary_value") or 0, reverse=not lower)
    span = max((r.get("primary_value") or 0) for r in live) if live else 0
    floor = min((r.get("primary_value") or 0) for r in live) if live else 0

    parts.append(
        '<div class="wrap"><table><thead><tr>'
        "<th>#</th><th></th><th>profile</th><th>heap</th>"
        f"<th>{html.escape(metric)}</th><th></th><th>vs base</th>"
        # MSPT median beside whichever percentile the workload is read by. On a
        # tick workload the primary column is already p95, so repeating it here
        # was one column saying the same thing twice; the median is the half
        # that says whether a collector stalls routinely or only rarely, and it
        # was in the JSON but displayed nowhere.
        "<th>MSPT med</th><th>TPS</th><th>RSS</th><th>CPU</th>"
        "<th>boot</th><th>GC95</th><th>GC99</th>"
        "</tr></thead><tbody>"
    )
    for i, row in enumerate(live + [r for r in rows if r.get("failed")], 1):
        is_base = row.get("profile") == BASELINE
        failed = bool(row.get("failed"))
        head = (
            ('<tr class="is-base">' if is_base else "<tr>")
            + cell("✕" if failed else f"{i:02d}", "rank")
            + cell("◆" if is_base else "", "mark")
            + cell(f"<code>{html.escape(row.get('profile', '?'))}</code>", "name")
            + cell(html.escape(row.get("heap") or EMPTY))
        )
        if failed:
            # An absence, not a score. Every metric would otherwise read zero,
            # and the delta against a working baseline a confident -100%.
            parts.append(head + '<td class="failed" colspan="10">FAILED</td></tr>')
            continue

        value = row.get("primary_value") or 0
        # Normalise across the observed range so the bars use their full width,
        # and invert when lower is better, so a longer bar always means better.
        frac = (value - floor) / (span - floor) if span > floor else 1.0
        cls = delta_class(row.get("vs_baseline"))
        parts.append(
            head
            + cell(f"{value:.1f}")
            + cell(bar(1.0 - frac if lower else frac), f"bar {cls}")
            + cell(html.escape(row.get("vs_baseline") or EMPTY), cls)
            + cell(num(row.get("mspt_median_ms"), ".1f", "ms"))
            + cell(num(row.get("tps"), ".1f"))
            + cell(human_bytes(row.get("peak_rss_bytes")))
            + cell(num(row.get("peak_cpu_percent"), ".0f", "%"))
            + cell(num(row.get("startup_sec"), ".1f", "s"))
            + cell(num(row.get("gc_pause_p95_ms"), ".0f", "ms"))
            + cell(num(row.get("gc_pause_p99_ms"), ".0f", "ms"))
            + "</tr>"
        )
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
            "WELOVEYOU · JVM BENCHMARKS",
            (primary.get("generated") or "")[:10] or "no data",
        )
    )
    parts.append(
        '<p class="sub prose">Which JVM, which collector, which flags. Measured on the '
        "little two-core box we actually run, because copying numbers off a "
        "four-year-old guide is how you end up confidently wrong.</p>"
    )

    if not (has_confirmed or has_screening):
        # An honest empty state beats a broken build. pages.yml also triggers when
        # its own files change, so the first run happens before any sweep merged.
        parts.append(
            '<div class="banner"><b>NO RESULTS YET</b><br>The sweep runs on a two-core '
            "Ampere A1 that stays powered off unless it is measuring, so numbers turn up "
            "in batches. Nothing is published until a human has read it and merged it.</div>"
        )
    else:
        n = sum(len(w.get("rows") or []) for w in (primary.get("workloads") or {}).values())
        wn = len(primary.get("workloads") or {})
        reps = primary.get("repeats_per_profile") or 0
        parts.append(
            '<div class="census">'
            f"<b>{n}</b> rows · <b>{wn}</b> workload{'s' if wn != 1 else ''} · "
            f"<b>{reps}</b> run{'s' if reps != 1 else ''} per profile</div>"
        )
        summary(primary, parts)

    if has_confirmed:
        sections(doc, parts)
    if has_screening:
        parts.append(
            '<div class="banner"><b>SCREENING PASS</b><br>One run per profile, so there '
            "is no variance to report and a single noisy neighbour moves a row. These "
            "exist to decide what is worth confirming at depth. They are "
            "<strong>not comparable to confirmed numbers</strong>.</div>"
        )
        sections(screening, parts)

    parts.append('<div class="caveats"><h3>How to read this</h3><ul>')
    parts += [f"<li>{c}</li>" for c in CAVEATS]
    parts.append("</ul></div>")
    parts.append(
        "<footer>Generated by <code>wly bench</code> from "
        '<a href="https://github.com/6586x57890143/weloveyou.mc">weloveyou.mc</a>. '
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
