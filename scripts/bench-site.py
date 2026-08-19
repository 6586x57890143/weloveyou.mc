#!/usr/bin/env python3
"""Render BENCHMARKS.json to a self-contained page for Cloudflare Pages.

    scripts/bench-site.py [--json BENCHMARKS.json] [--out site]

Reads the machine-readable twin that `wly bench` writes rather than parsing the
Markdown back: the numbers are published after a human has checked and merged
them, by which point the run is long gone, and re-parsing our own prose would be
fragile in the one place fragility is least visible.

Stdlib only, and one file out with no external assets. The point is to publish
numbers, not to acquire a site generator.
"""
import argparse
import html
import json
import pathlib
import sys

CSS = """
:root{--bg:#fbfbfa;--fg:#1a1a18;--dim:#6b6b64;--line:#e3e3dd;--accent:#2f6f4f;--warn:#8a5a1a;--card:#fff}
@media(prefers-color-scheme:dark){:root{--bg:#131313;--fg:#e8e8e3;--dim:#96968c;--line:#2b2b28;--accent:#7fd0a3;--warn:#d9a55a;--card:#1a1a19}}
*{box-sizing:border-box}
body{margin:0;padding:2.5rem 1.25rem 4rem;background:var(--bg);color:var(--fg);
 font:16px/1.6 ui-sans-serif,system-ui,-apple-system,"Segoe UI",sans-serif}
main{max-width:60rem;margin:0 auto}
h1{font-size:1.75rem;margin:0 0 .25rem;letter-spacing:-.02em}
h2{font-size:1.15rem;margin:2.5rem 0 .25rem;letter-spacing:-.01em}
.sub{color:var(--dim);margin:0 0 2rem}
.meta{display:flex;flex-wrap:wrap;gap:.4rem 1.5rem;color:var(--dim);font-size:.875rem;margin:0 0 2rem}
.note{color:var(--dim);font-size:.9rem;margin:.35rem 0 1rem}
.wrap{overflow-x:auto;border:1px solid var(--line);border-radius:.5rem;background:var(--card)}
table{border-collapse:collapse;width:100%;font-variant-numeric:tabular-nums;font-size:.925rem}
th,td{padding:.6rem .85rem;text-align:right;white-space:nowrap;border-bottom:1px solid var(--line)}
th:first-child,td:first-child{text-align:left}
tbody tr:last-child td{border-bottom:0}
thead th{font-weight:600;font-size:.8rem;text-transform:uppercase;letter-spacing:.04em;color:var(--dim)}
code{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:.9em}
.win{color:var(--accent);font-weight:600}
.lose{color:var(--warn)}
.caveats{border:1px solid var(--line);border-left:3px solid var(--warn);border-radius:.4rem;
 background:var(--card);padding:1rem 1.25rem;margin:2.5rem 0 0}
.caveats h3{margin:0 0 .5rem;font-size:.95rem}
.caveats li{margin:.35rem 0;color:var(--dim);font-size:.9rem}
footer{margin-top:3rem;color:var(--dim);font-size:.85rem;border-top:1px solid var(--line);padding-top:1rem}
a{color:inherit}
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
    "<strong>MSPT is the number players feel.</strong> A tick is 50ms; a 95th "
    "percentile above that means the server is routinely behind, however good its "
    "chunk throughput looks. Measured with spark, whose background profiler is "
    "disabled, it segfaults the JVM on Java 25/aarch64.",
    "<strong>Heap is a dimension, not a constant.</strong> The useful question is not "
    "whether the server can use the whole box, it is where more memory stops buying "
    "throughput.",
]


def cell_delta(text):
    """Colour a vs-baseline cell. `~` means inside the noise floor, so neither."""
    t = html.escape(text)
    if t.startswith("+"):
        return f'<td class="win">{t}</td>'
    if t.startswith("-"):
        return f'<td class="lose">{t}</td>'
    return f"<td>{t}</td>"


def cell_mspt(ms):
    """95th-percentile tick duration. A tick is 50ms; past that the server is
    behind and players feel it, so that is where this turns red."""
    if not ms:
        return "<td>, </td>"
    cls = ' class="lose"' if ms >= 50 else ""
    return f"<td{cls}>{ms:.1f}ms</td>"


def cell_tps(tps):
    """20 is the ceiling, not a target. Anything below it is the server failing
    to keep up, which throughput alone will not tell you."""
    if not tps:
        return "<td>, </td>"
    cls = ' class="lose"' if tps < 19.5 else ' class="win"'
    return f"<td{cls}>{tps:.1f}</td>"


def cell_cpu(pct):
    """Docker reports CPU relative to one core, so 200% is both cores saturated.

    Flagged when a profile leaves a core idle: on a two-core box that usually
    means the collector is not using the parallelism it was given, which is the
    interesting half of a disappointing throughput number.
    """
    if not pct:
        return "<td>, </td>"
    cls = ' class="lose"' if pct < 150 else ""
    return f"<td{cls}>{pct:.0f}%</td>"


def human_bytes(n):
    if not n:
        return ", "
    for unit in ("B", "KiB", "MiB", "GiB", "TiB"):
        if n < 1024 or unit == "TiB":
            return f"{n:.2f} {unit}" if unit not in ("B", "KiB") else f"{n:.0f} {unit}"
        n /= 1024


def render(doc):
    workloads = doc.get("workloads") or {}
    parts = [
        "<main>",
        "<h1>weloveyou 💖 JVM benchmarks</h1>",
        '<p class="sub">Which JVM, which collector, which flags. Measured on the '
        "little two-core box we actually run, because copying numbers off a "
        "four-year-old guide is how you end up confidently wrong.</p>",
    ]
    if workloads:
        parts.append(
            '<div class="meta">'
            f"<span>host <code>{html.escape(doc.get('host', '?'))}</code></span>"
            f"<span>generated {html.escape(doc.get('generated', '?'))}</span>"
            f"<span>{doc.get('repeats_per_profile', 0)} run(s) per profile</span>"
            "</div>"
        )
    else:
        # An honest empty state beats a broken build. The page exists from the
        # moment publishing is wired up, and says what it is waiting for.
        parts.append(
            '<div class="caveats"><h3>No results yet</h3>'
            "<ul><li>The sweep runs on a two-core Ampere A1 that stays powered off "
            "unless it is measuring, so numbers turn up in batches.</li>"
            "<li>Nothing gets published until a human has read it and merged it. "
            "No results straight off a run.</li></ul></div>"
        )

    for key in ("vanilla", "pack"):
        wl = workloads.get(key)
        if not wl:
            continue
        parts.append(f"<h2>{html.escape(wl.get('title', key))}</h2>")
        mods = wl.get("mods_loaded") or 0
        if mods:
            note = (
                f"{mods} mods loaded, this pack grows, so these rows are only "
                "comparable to runs against a similar pack."
                if key == "pack"
                else f"{mods} mods loaded (Fabric API and the pregenerator)."
            )
            parts.append(f'<p class="note">{note}</p>')
        parts.append(
            '<div class="wrap"><table><thead><tr>'
            "<th>profile</th><th>heap</th><th>chunks/s</th><th>vs base</th>"
            "<th>MSPT p95</th><th>TPS</th>"
            "<th>peak RSS</th><th>peak CPU</th><th>startup</th>"
            "<th>GC p95</th><th>GC p99</th>"
            "</tr></thead><tbody>"
        )
        for row in wl.get("rows") or []:
            parts.append(
                "<tr>"
                f"<td><code>{html.escape(row.get('profile', '?'))}</code></td>"
                f"<td>{html.escape(row.get('heap') or ', ')}</td>"
                f"<td>{row.get('chunks_per_sec', 0):.1f}</td>"
                + cell_delta(row.get("vs_baseline", ""))
                + cell_mspt(row.get("mspt_p95_ms") or 0)
                + cell_tps(row.get("tps") or 0)
                + f"<td>{human_bytes(row.get('peak_rss_bytes') or 0)}</td>"
                + cell_cpu(row.get("peak_cpu_percent") or 0)
                + f"<td>{row.get('startup_sec', 0):.1f}s</td>"
                f"<td>{row.get('gc_pause_p95_ms', 0):.1f}ms</td>"
                f"<td>{row.get('gc_pause_p99_ms', 0):.1f}ms</td>"
                "</tr>"
            )
        parts.append("</tbody></table></div>")

        dropped = sorted({f for r in wl.get("rows") or [] for f in (r.get("dropped_flags") or [])})
        if dropped:
            flags = ", ".join(f"<code>{html.escape(f)}</code>" for f in dropped)
            parts.append(
                f'<p class="note">Refused by the JVM and dropped before the run: {flags}. '
                "The preflight asks the JVM rather than trusting the guide.</p>"
            )

    parts.append('<div class="caveats"><h3>How to read this</h3><ul>')
    parts.extend(f"<li>{c}</li>" for c in CAVEATS)
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
        "<title>weloveyou 💖 JVM benchmarks</title>"
        f"<style>{CSS}</style></head><body>\n{body}\n</body></html>\n"
    )


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--json", default="BENCHMARKS.json")
    ap.add_argument("--out", default="site")
    args = ap.parse_args()

    src = pathlib.Path(args.json)
    if src.exists():
        doc = json.loads(src.read_text(encoding="utf-8"))
    else:
        # Not an error. pages.yml also triggers when its own files change, so
        # the very first run happens before any sweep has been merged, and
        # failing there makes a perfectly good merge look broken. Publishing
        # nothing is not a failure when there is nothing to publish, so the
        # site goes up saying exactly that and the first real sweep replaces it.
        print(f"::notice::{src} does not exist yet; publishing a placeholder")
        doc = {}
    doc = doc or {}

    outdir = pathlib.Path(args.out)
    outdir.mkdir(parents=True, exist_ok=True)
    (outdir / "index.html").write_text(render(doc), encoding="utf-8", newline="\n")
    # The data alongside the page: anyone comparing our numbers to their own
    # should not have to scrape a table to do it.
    (outdir / "benchmarks.json").write_text(
        json.dumps(doc, indent=2) + "\n", encoding="utf-8", newline="\n"
    )
    print(f"wrote {outdir/'index.html'} and {outdir/'benchmarks.json'}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
