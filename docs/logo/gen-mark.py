#!/usr/bin/env python3
"""Generate the square "OT" monogram (avatars, favicons, app icons) in opentree's
pixel/terminal style — a bolder cut than the wordmark so it holds up small.

Usage: gen-mark.py OUT.svg [--bg C] [--o-fg C] [--t-fg C] [--fg C] [--px N] [--pad N]
  --fg sets both letters one colour; --o-fg/--t-fg set them separately
  (default echoes the wordmark: O in near-white "open", T in amber "tree").
"""
import sys

# bold pixel letters — '#' = filled cell, '.' = empty. 7 wide x 9 tall each.
O = [
    ".#####.",
    "#######",
    "##...##",
    "##...##",
    "##...##",
    "##...##",
    "##...##",
    "#######",
    ".#####.",
]
T = [
    "#######",
    "#######",
    "..###..",
    "..###..",
    "..###..",
    "..###..",
    "..###..",
    "..###..",
    "..###..",
]
GAP = 2                      # empty columns between O and T
LW = len(O[0]) + GAP + len(T[0])   # letter block width in cells (16)
LH = len(O)                        # letter block height in cells (9)


def rects(matrix, colour, cx, cy, px):
    out = []
    for r, line in enumerate(matrix):
        for c, ch in enumerate(line):
            if ch == "#":
                out.append(f'<rect x="{(cx+c)*px}" y="{(cy+r)*px}" width="{px}" height="{px}" fill="{colour}"/>')
    return out


def main():
    out = sys.argv[1]
    def opt(name, default):
        return sys.argv[sys.argv.index(name)+1] if name in sys.argv else default
    bg   = opt("--bg", "#0d0f14")
    fg   = opt("--fg", None)
    o_fg = fg or opt("--o-fg", "#eceef2")
    t_fg = fg or opt("--t-fg", "#F4A261")
    px   = int(opt("--px", "28"))
    pad  = int(opt("--pad", "4"))          # cells of breathing room around the letters

    side_cells = LW + 2 * pad              # square tile, sized off the wider (letter) axis
    side = side_cells * px
    x_off = pad                            # centre the letter block
    y_off = (side_cells - LH) / 2

    body = [f'<rect x="0" y="0" width="{side}" height="{side}" rx="{px*3:g}" fill="{bg}"/>']
    body += rects(O, o_fg, x_off, y_off, px)
    body += rects(T, t_fg, x_off + len(O[0]) + GAP, y_off, px)

    svg = (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {side} {side}" '
           f'width="{side}" height="{side}" shape-rendering="crispEdges" '
           f'role="img" aria-label="opentree OT monogram">\n  ' + "\n  ".join(body) + "\n</svg>\n")
    with open(out, "w") as f:
        f.write(svg)
    print(f"wrote {out}  ({side}x{side}, o={o_fg}, t={t_fg})")


if __name__ == "__main__":
    main()
