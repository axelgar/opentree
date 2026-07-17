#!/usr/bin/env python3
"""Generate the opentree wordmark logo as SVG, faithfully from the TUI's
block-pixel renderLogo() (pkg/tui/helpers.go).

Each source character is a 1-wide, 2-tall pixel cell (half-block glyphs):
  █ full   -> top=fg, bottom=fg        _ bg-space -> top=bg, bottom=bg
  ▀ upper  -> top=fg, bottom=trans     ^ fg-on-bg -> top=fg, bottom=bg
  ▄ lower  -> top=trans, bottom=fg     ~ shadow   -> top=shadow, bottom=trans
  (space)  -> transparent

Output row = left("open") + one gap column + right("tree") = 39 cols x 8 px-rows.

Usage: gen-logo.py OUT.svg [--flat] [--bg COLOR] [--px N] [--pad CELLS]
  --flat : letterforms only (drop the grey depth cells) for a clean two-tone mark
  --bg   : background colour, or "none" for transparent (default #0d0f14)
"""
import sys

# --- verbatim copy of the glyph grid from pkg/tui/helpers.go (renderLogo) -----
GLYPHS_LEFT = [
    "                   ",
    "█▀▀█ █▀▀█ █▀▀█ █▀▀▄",
    "█__█ █__█ █^^^ █__█",
    "▀▀▀▀ █▀▀▀ ▀▀▀▀ ▀~~▀",
]
GLYPHS_RIGHT = [
    " ▄               ",
    "▀█▀▀ █▀▀▄ █▀▀█ █▀▀█",
    "_█__ █^^^ █^^^ █^^^",
    "_▀▀▀ ▀    ▀▀▀▀ ▀▀▀▀",
]

# panel colours: (fg, bg, shadow). Left "open" is dim, right "tree" is bright.
# bg/shadow reproduce the TUI's 256-colour greys (235 = #262626, 238 = #444444).
LEFT  = {"fg": "#8a8a8a", "bg": "#262626", "shadow": "#262626"}
RIGHT = {"fg": "#f2f2f2", "bg": "#444444", "shadow": "#444444"}

GAP = 1  # transparent columns between the two panels
WIDTH_CELLS = 19


def cell_pixels(ch, c, flat):
    """(top, bottom) colours for a cell; None = transparent."""
    fg, bg, shadow = c["fg"], c["bg"], c["shadow"]
    if flat:  # letterforms only — depth cells become transparent
        bg = shadow = None
    return {
        "█": (fg, fg),
        "▀": (fg, None),
        "▄": (None, fg),
        "_":      (bg, bg),
        "^":      (fg, bg),
        "~":      (shadow, None),
    }.get(ch, (None, None))


def rects(px, pad, flat):
    out = []
    def emit(row_cells, colours, col_off):
        for r, line in enumerate(row_cells):
            for col, ch in enumerate(line):
                top, bot = cell_pixels(ch, colours, flat)
                x = (col_off + col) * px + pad
                for half, colour in ((0, top), (1, bot)):
                    if colour:
                        y = (r * 2 + half) * px + pad
                        out.append(f'<rect x="{x}" y="{y}" width="{px}" height="{px}" fill="{colour}"/>')
    emit(GLYPHS_LEFT, LEFT, 0)
    emit(GLYPHS_RIGHT, RIGHT, WIDTH_CELLS + GAP)
    return out


def main():
    out_path = sys.argv[1]
    flat = "--flat" in sys.argv
    bg = "none"
    px, pad_cells = 22, 2.0
    if "--bg" in sys.argv:  bg = sys.argv[sys.argv.index("--bg") + 1]
    if "--px" in sys.argv:  px = int(sys.argv[sys.argv.index("--px") + 1])
    if "--pad" in sys.argv: pad_cells = float(sys.argv[sys.argv.index("--pad") + 1])
    # optional recolour of the "open" / "tree" letterforms (defaults = TUI greys)
    if "--open-fg" in sys.argv: LEFT["fg"]  = sys.argv[sys.argv.index("--open-fg") + 1]
    if "--tree-fg" in sys.argv: RIGHT["fg"] = sys.argv[sys.argv.index("--tree-fg") + 1]

    cols = WIDTH_CELLS * 2 + GAP
    pad = pad_cells * px
    w = cols * px + 2 * pad
    h = 8 * px + 2 * pad
    body = []
    if bg != "none":
        radius = px * 1.2
        body.append(f'<rect x="0" y="0" width="{w:g}" height="{h:g}" rx="{radius:g}" fill="{bg}"/>')
    body += rects(px, pad, flat)
    svg = (
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w:g} {h:g}" '
        f'width="{w:g}" height="{h:g}" shape-rendering="crispEdges" '
        f'role="img" aria-label="opentree">\n  ' + "\n  ".join(body) + "\n</svg>\n"
    )
    with open(out_path, "w") as f:
        f.write(svg)
    print(f"wrote {out_path}  ({w:g}x{h:g}, flat={flat}, bg={bg})")


if __name__ == "__main__":
    main()
