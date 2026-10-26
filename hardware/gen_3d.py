#!/usr/bin/env python3
"""
pingzap ESP32 shield - 3D render.

Builds a 3D model of the board from the same placement as gen_pcb.py (pads,
tracks, ground pour and silkscreen come from there) with simplified models of
each part and the ESP32 DevKit plugged in, and writes:
  render-3d.png           the assembled board, seen from the front

Coordinates follow gen_pcb.py (mm, y pointing down); the model flips y so
that the board is seen as on the screen.

Requirements: pyvista, mapbox_earcut, plus the gen_pcb.py ones
(pip install -r requirements.txt). Rendering is off-screen.
"""

import math
import os

import mapbox_earcut as earcut
import numpy as np
import pyvista as pv
from shapely.geometry import MultiPolygon, Polygon
from shapely.ops import unary_union

import gen_pcb as pcb

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "render-3d.png")

T = 1.6  # board thickness
SKIN = 0.04  # copper and silkscreen height above the board

C_SUBSTRATE = "#0c4a2a"
C_MASK = "#17703d"
C_PAD = "#d4d4d4"
C_SILK = "#f4f4f4"
C_BLACK = "#1c1c1e"
C_BLUE = "#1f5fbf"
C_METAL = "#b8b8bc"
C_GOLD = "#d8b25a"
C_BG = "#eef1f4"


def P(x, y, z=0.0):
    return (x, -y, z)


# --------------------------------------------------------------------------
# Mesh helpers
# --------------------------------------------------------------------------

def flat(geom, z0, h):
    """Extrude a shapely (Multi)Polygon from z0 to z0 + h."""
    polys = geom.geoms if isinstance(geom, MultiPolygon) else [geom]
    meshes = []
    for poly in polys:
        if poly.is_empty or not isinstance(poly, Polygon):
            continue
        rings = [np.asarray(poly.exterior.coords)[:-1]] + [np.asarray(r.coords)[:-1] for r in poly.interiors]
        verts = np.vstack(rings)
        ends = np.cumsum([len(r) for r in rings]).astype(np.uint32)
        tris = earcut.triangulate_float64(verts, ends).reshape(-1, 3)
        if not len(tris):
            continue
        n = len(verts)
        pts = np.vstack([np.c_[verts[:, 0], -verts[:, 1], np.full(n, z0)],
                         np.c_[verts[:, 0], -verts[:, 1], np.full(n, z0 + h)]])
        faces = [np.c_[np.full(len(tris), 3), tris[:, ::-1]], np.c_[np.full(len(tris), 3), tris + n]]
        start = 0
        for r in rings:  # side walls
            m = len(r)
            i = np.arange(start, start + m)
            j = np.roll(i, -1)
            faces.append(np.c_[np.full(m, 4), i, j, j + n, i + n])
            start += m
        meshes.append(pv.PolyData(pts, np.hstack([f.ravel() for f in faces])))
    return meshes


def box(x0, y0, x1, y1, z0, z1):
    return pv.Box(bounds=(x0, x1, -y1, -y0, z0, z1))


def cyl(x, y, z0, h, r, res=40):
    return pv.Cylinder(center=P(x, y, z0 + h / 2), direction=(0, 0, 1), radius=r, height=h, resolution=res)


def rod(a, b, r, res=24):
    a, b = np.asarray(a, float), np.asarray(b, float)
    return pv.Cylinder(center=(a + b) / 2, direction=b - a, radius=r, height=np.linalg.norm(b - a), resolution=res)


def label(s, x, y, z, height, angle=0.0):
    """Flat text lying on z, centred on (x, y), reading along +x."""
    t = pv.Text3D(s, depth=0.05)
    x0, x1, y0, y1, _, _ = t.bounds
    t.translate((-(x0 + x1) / 2, -(y0 + y1) / 2, 0), inplace=True)
    t.scale(height / (y1 - y0), inplace=True)
    if angle:
        t.rotate_z(angle, inplace=True)
    t.translate(P(x, y, z), inplace=True)
    return t


class Scene:
    def __init__(self):
        self.items = []

    def add(self, mesh, color, **kw):
        meshes = mesh if isinstance(mesh, list) else [mesh]
        self.items += [(m, color, kw) for m in meshes]


# --------------------------------------------------------------------------
# Parts
# --------------------------------------------------------------------------

def leg(s, x, y, top):
    s.add(rod(P(x, y, T), P(x, y, top), 0.3, 12), C_METAL, metallic=True)


def axial(s, part):
    a, b = part.pads
    ux, uy = b.x - a.x, b.y - a.y
    length = math.hypot(ux, uy)
    ux, uy = ux / length, uy / length
    body_len, r = {"D_DO-41_P10.16mm": (5.2, 1.35), "D_DO-35_P12.00mm": (3.8, 0.9),
                   "D_DO-35_P7.62mm": (3.8, 0.9)}.get(part.footprint, (6.3, 1.25))
    zc = T + 1.0 + r
    mx, my = (a.x + b.x) / 2, (a.y + b.y) / 2
    hl = body_len / 2
    for p, sgn in ((a, -1), (b, 1)):
        leg(s, p.x, p.y, zc)
        s.add(rod(P(p.x, p.y, zc), P(mx + sgn * ux * hl, my + sgn * uy * hl, zc), 0.3, 12), C_METAL, metallic=True)
    body = rod(P(mx - ux * hl, my - uy * hl, zc), P(mx + ux * hl, my + uy * hl, zc), r)
    if part.ref.startswith("D"):
        glass = part.value == "1N4148"
        s.add(body, "#c8532b" if glass else C_BLACK, opacity=0.85 if glass else 1.0)
        # cathode band at pad 1
        cx, cy = mx - ux * (hl - 0.6), my - uy * (hl - 0.6)
        s.add(rod(P(cx - ux * 0.35, cy - uy * 0.35, zc), P(cx + ux * 0.35, cy + uy * 0.35, zc), r + 0.05),
              C_BLACK if glass else "#d9d9d9")
        return
    s.add(body, "#d8c49a")
    bands = {"1k": ["#7a4b26", C_BLACK, "#c8302c"], "10k": ["#7a4b26", C_BLACK, "#e07b21"],
             "330": ["#e07b21", "#e07b21", "#7a4b26"]}[part.value] + [C_GOLD]
    for i, color in enumerate(bands):
        t = -hl + 0.9 + i * 1.1 + (0.6 if i == 3 else 0)
        cx, cy = mx + ux * t, my + uy * t
        s.add(rod(P(cx - ux * 0.3, cy - uy * 0.3, zc), P(cx + ux * 0.3, cy + uy * 0.3, zc), r + 0.04), color)


def to92(s, part):
    e, b, c = part.pads
    body = Polygon([(b.x + 2.3 * math.cos(t), b.y + 2.3 * math.sin(t)) for t in np.linspace(0, 2 * math.pi, 48)])
    body = body.intersection(Polygon([(b.x - 3, b.y - 1.4), (b.x + 3, b.y - 1.4), (b.x + 3, b.y + 3), (b.x - 3, b.y + 3)]))
    for p in (e, b, c):
        leg(s, p.x, p.y, T + 3.0)
    s.add(flat(body, T + 3.0, 4.6), C_BLACK)


def led5(s, part, color):
    k, a = part.pads
    cx, cy = (a.x + k.x) / 2, (a.y + k.y) / 2
    z0 = T + 2.0
    leg(s, k.x, k.y, z0)
    leg(s, a.x, a.y, z0)
    s.add(cyl(cx, cy, z0, 1.0, 2.9), color, opacity=0.9)
    s.add(cyl(cx, cy, z0 + 1.0, 4.6, 2.5), color, opacity=0.8)
    s.add(pv.Sphere(radius=2.5, center=P(cx, cy, z0 + 5.6), theta_resolution=40, phi_resolution=20,
                    end_phi=90), color, opacity=0.8)


def cap(s, part, sleeve):
    p, m = part.pads
    cx, cy = (p.x + m.x) / 2, (p.y + m.y) / 2
    d = 6.3
    s.add(cyl(cx, cy, T + 0.3, 11.0, d / 2), sleeve)
    s.add(cyl(cx, cy, T + 11.3, 0.05, d / 2 - 0.4), C_METAL, metallic=True)
    # minus stripe on the side of pad 2
    ang = math.atan2(m.y - cy, m.x - cx)
    sx, sy = cx + math.cos(ang) * (d / 2 - 0.05), cy + math.sin(ang) * (d / 2 - 0.05)
    s.add(cyl(sx, sy, T + 0.6, 10.4, 0.9, 24), "#cfd4dc")


def buzzer(s, part):
    p, m = part.pads
    cx, cy = (p.x + m.x) / 2, (p.y + m.y) / 2
    s.add(cyl(cx, cy, T, 9.5, 6.0, 64), C_BLACK)
    s.add(cyl(cx, cy, T + 9.5, 0.02, 1.0), "#050505")
    s.add(label("+", p.x, p.y - 2.6, T + 9.52, 1.6), C_SILK)


def dip4(s, part):
    xs = [p.x for p in part.pads]
    ys = [p.y for p in part.pads]
    x0, x1, y0, y1 = min(xs), max(xs), min(ys), max(ys)
    for p in part.pads:
        leg(s, p.x, p.y, T + 1.0)
    s.add(box(x0 + 0.9, y0 - 1.9, x1 - 0.9, y1 + 1.9, T + 0.8, T + 4.3), C_BLACK)
    s.add(cyl(x0 + 2.0, y0 - 0.6, T + 4.3, 0.02, 0.5), "#3a3a3c")
    s.add(label("PC817", (x0 + x1) / 2, (y0 + y1) / 2, T + 4.32, 0.9), "#bdbdbd")


def sip3(s, part):
    p1 = part.pads[0]
    x, y = p1.x, p1.y
    for p in part.pads:
        leg(s, p.x, p.y, T + 1.5)
    s.add(box(x - 3.2, y - 2.0, x + 8.28, y + 2.6, T + 1.5, T + 11.5), C_BLACK)
    s.add(label("K7805-1000R3", x + 2.54, y + 0.3, T + 11.52, 0.9), "#bdbdbd")


def terminal(s, part):
    xs = part.pads[0].x
    ys = [p.y for p in part.pads]
    y0, y1 = min(ys) - 2.54, max(ys) + 2.54
    s.add(box(xs - 5.0, y0, xs + 4.6, y1, T, T + 10.0), "#2a7bd4")
    for y in ys:
        s.add(cyl(xs - 1.0, y, T + 10.0, 0.4, 1.7), C_METAL, metallic=True)
        s.add(box(xs - 2.3, y - 0.25, xs + 0.3, y + 0.25, T + 10.35, T + 10.45), "#555555")
        s.add(box(xs + 4.55, y - 1.6, xs + 4.65, y + 1.6, T + 2.0, T + 5.5), "#0d2f57")  # wire entry


def header(s, part, female):
    xs = [p.x for p in part.pads]
    ys = [p.y for p in part.pads]
    x0, x1, y0, y1 = min(xs) - 1.27, max(xs) + 1.27, min(ys) - 1.27, max(ys) + 1.27
    if female:
        s.add(box(x0, y0, x1, y1, T, T + 8.5), C_BLACK)
        for p in part.pads:
            s.add(box(p.x - 0.5, p.y - 0.5, p.x + 0.5, p.y + 0.5, T + 8.5, T + 8.52), "#000000")
        return
    s.add(box(x0, y0, x1, y1, T, T + 2.5), C_BLACK)
    for p in part.pads:
        s.add(box(p.x - 0.32, p.y - 0.32, p.x + 0.32, p.y + 0.32, T + 2.5, T + 8.5), C_GOLD, metallic=True)


def relay(s, part):
    com = part.pads[0]
    x, y = com.x, com.y
    s.add(box(x - 1.6, y - 7.75, x + 17.4, y + 7.75, T, T + 15.3), C_BLUE)
    z = T + 15.32
    s.add(label("SONGLE", x + 7.9, y - 4.6, z, 2.0), C_SILK)
    s.add(label("SRD-05VDC-SL-C", x + 7.9, y - 1.0, z, 1.3), C_SILK)
    s.add(label("10A 250VAC  10A 30VDC", x + 7.9, y + 2.0, z, 0.9), C_SILK)
    s.add(label("10A 125VAC  10A 28VDC", x + 7.9, y + 3.6, z, 0.9), C_SILK)


def esp32(s):
    """DOIT ESP32 DevKit V1 (30 pins) on the two sockets, antenna past the top edge."""
    lx, rx = 8.0, 33.4
    cx = (lx + rx) / 2
    y_pin0, y_pin14 = 6.0, 6.0 + 2.54 * 14
    zb = T + 8.5 + 2.5  # socket top + header plastic under the module
    top = zb + 1.6
    w, y0, y1 = 28.3, y_pin0 - 10.5, y_pin14 + 5.0
    for x in (lx, rx):  # male header plastic between socket and module
        s.add(box(x - 1.27, y_pin0 - 1.27, x + 1.27, y_pin14 + 1.27, T + 8.5, zb), C_BLACK)
    s.add(box(cx - w / 2, y0, cx + w / 2, y1, zb, top), "#1d1f24")
    for x in (lx, rx):
        for i in range(15):
            s.add(cyl(x, y_pin0 + 2.54 * i, top, 0.6, 0.55, 16), C_GOLD, metallic=True)
    # ESP-WROOM-32: module PCB with antenna, metal shield
    s.add(box(cx - 9.0, y0, cx + 9.0, y0 + 25.5, top, top + 0.8), "#202226")
    s.add(box(cx - 8.0, y0 + 6.6, cx + 8.0, y0 + 24.2, top + 0.8, top + 3.1), "#c4c6ca", metallic=True)
    s.add(label("ESP32-WROOM-32", cx, y0 + 12.5, top + 3.12, 1.0), "#4a4a4e")
    s.add(label("WiFi + Bluetooth", cx, y0 + 15.0, top + 3.12, 0.8), "#4a4a4e")
    ant = [(cx - 7.5, y0 + 1.2), (cx + 7.5, y0 + 1.2), (cx + 7.5, y0 + 2.4), (cx - 6.5, y0 + 2.4),
           (cx - 6.5, y0 + 3.6), (cx + 7.5, y0 + 3.6), (cx + 7.5, y0 + 4.8), (cx - 7.5, y0 + 4.8)]
    for (ax, ay), (bx, by) in zip(ant, ant[1:]):
        s.add(flat(pcb.track_geom(0.5, [(ax, ay), (bx, by)]), top + 0.8, 0.03), C_GOLD)
    # USB, buttons, regulator, USB-serial chip
    s.add(box(cx - 4.0, y1 - 5.0, cx + 4.0, y1 + 0.8, top, top + 2.8), C_METAL, metallic=True)
    for bx, txt in ((cx - 9.3, "EN"), (cx + 9.3, "BOOT")):
        s.add(box(bx - 1.9, y1 - 6.2, bx + 1.9, y1 - 2.6, top, top + 1.6), C_METAL, metallic=True)
        s.add(cyl(bx, y1 - 4.4, top + 1.6, 0.5, 1.0), C_BLACK)
        s.add(label(txt, bx, y1 - 7.4, top + 0.02, 0.8), C_SILK)
    s.add(box(cx - 1.5, y1 - 13.0, cx + 1.5, y1 - 9.5, top, top + 1.6), C_BLACK)
    s.add(box(cx - 2.5, y1 - 22.0, cx + 2.5, y1 - 17.0, top, top + 0.9), C_BLACK)
    s.add(cyl(cx + 6.5, y1 - 13.0, top, 0.5, 0.6), "#ff4b3e")
    s.add(cyl(cx - 6.5, y1 - 13.0, top, 0.5, 0.6), "#4a8cff")


# --------------------------------------------------------------------------
# Board
# --------------------------------------------------------------------------

def build_scene():
    parts, tracks, holes, silk = pcb.build()
    feats = pcb.copper_features(parts, tracks)
    pours = {layer: pcb.ground_pour(layer, feats, parts, holes) for layer in ("F", "B")}
    pads = [p for part in parts for p in part.pads]
    s = Scene()

    outline = pcb.board_outline()
    for x, y, d in holes:
        outline = outline.difference(pcb.Point(x, y).buffer(d / 2, 32))
    s.add(flat(outline, 0, T), C_SUBSTRATE)
    copper = unary_union([pours["F"]] + [pcb.track_geom(w, pts) for l, n, w, pts in tracks if l == "F"])
    s.add(flat(copper.difference(unary_union([p.geom() for p in pads])), T, SKIN / 2), C_MASK)
    pad_geom = unary_union([p.geom() for p in pads]).difference(
        unary_union([pcb.Point(p.x, p.y).buffer(p.drill / 2, 16) for p in pads]))
    s.add(flat(pad_geom, T, SKIN), C_PAD, metallic=True)
    for x, y, d in holes:
        s.add(flat(pcb.Point(x, y).buffer(d / 2 + 1.2, 32).difference(pcb.Point(x, y).buffer(d / 2, 32)),
                   T, SKIN), C_PAD, metallic=True)

    silk_geom = unary_union(silk + [g for part in parts for g in part.silk] + [
        pcb.text(part.ref, *part.label) for part in parts if part.label])
    silk_geom = silk_geom.difference(unary_union([p.geom(pcb.MASK_EXPANSION + 0.1) for p in pads]))
    s.add(flat(silk_geom.intersection(pcb.board_outline().buffer(-0.2)), T, SKIN * 1.5), C_SILK)

    for part in parts:
        fp = part.footprint
        if fp.startswith("R_") or fp.startswith("D_"):
            axial(s, part)
        elif fp.startswith("TO-92"):
            to92(s, part)
        elif fp.startswith("LED"):
            led5(s, part, "#ff3b30" if part.value == "Vermelho" else "#34c759")
        elif fp.startswith("CP_"):
            cap(s, part, C_BLACK if part.ref == "C1" else "#25407a")
        elif fp.startswith("Buzzer"):
            buzzer(s, part)
        elif fp.startswith("DIP"):
            dip4(s, part)
        elif fp.startswith("SIP"):
            sip3(s, part)
        elif fp.startswith("TerminalBlock"):
            terminal(s, part)
        elif fp.startswith("PinSocket"):
            header(s, part, True)
        elif fp.startswith("PinHeader"):
            header(s, part, False)
        elif fp.startswith("Relay"):
            relay(s, part)
        else:
            raise ValueError(f"no 3D model for {part.ref} ({fp})")
    esp32(s)
    return s


def render(s, path):
    pl = pv.Plotter(off_screen=True, window_size=(2400, 1600), lighting="none")
    pl.set_background(C_BG)
    pl.enable_anti_aliasing("ssaa")
    for mesh, color, kw in s.items:
        metallic = kw.pop("metallic", False)
        pl.add_mesh(mesh, color=color, smooth_shading=False, specular=0.6 if metallic else 0.15,
                    specular_power=30 if metallic else 10, ambient=0.25, diffuse=0.8, **kw)
    pl.add_light(pv.Light(position=(-60, -140, 220), focal_point=(50, -35, 0), intensity=0.85))
    pl.add_light(pv.Light(position=(180, 40, 120), focal_point=(50, -35, 0), intensity=0.35))
    pl.add_light(pv.Light(position=(50, 150, 60), focal_point=(50, -35, 0), intensity=0.2))
    pl.camera.focal_point = (50, -33, 4)
    pl.camera.position = (50 - 50, -37 - 138, 106)
    pl.camera.up = (0, 0, 1)
    pl.camera.view_angle = 30
    img = pl.screenshot(return_img=True)
    pl.close()
    # Trim the empty background, keeping a margin.
    bg = np.array(pv.Color(C_BG).int_rgb)
    rows, cols = np.where(np.abs(img[:, :, :3].astype(int) - bg).sum(axis=2) > 12)
    m = 40
    img = img[max(rows.min() - m, 0):rows.max() + m, max(cols.min() - m, 0):cols.max() + m]
    import matplotlib.pyplot as plt
    plt.imsave(path, img)


def main():
    render(build_scene(), OUT)
    print(f"wrote {os.path.relpath(OUT, HERE)}")


if __name__ == "__main__":
    main()
