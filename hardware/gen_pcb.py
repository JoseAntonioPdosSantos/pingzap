#!/usr/bin/env python3
"""
pingzap ESP32 shield - PCB generator.

Builds the board from the placement and routing below and writes:
  gerber/                 Gerber X2 + Excellon files (what PCBWay asks for)
  pingzap-shield-gerber.zip
  bom.csv                 bill of materials
  preview-top.png / preview-bottom.png

It also runs its own DRC (clearances, annular rings, edge distances) and a
connectivity check (every net is one piece of copper, no shorts) and stops
on any error, so the files are only written for a board that passes.

Coordinates are in mm, origin at the top-left corner, y pointing down (as on
the screen); the Gerber writer flips y.

Requirements: shapely, matplotlib, numpy (pip install -r requirements.txt).
"""

import csv
import math
import os
import sys
import zipfile
from collections import defaultdict

from matplotlib.font_manager import FontProperties
from matplotlib.textpath import TextPath
from shapely.geometry import LineString, MultiPolygon, Point, Polygon, box
from shapely.ops import split, unary_union

HERE = os.path.dirname(os.path.abspath(__file__))
NAME = "pingzap-shield"
BOARD_W, BOARD_H = 100.0, 70.0
CORNER_R = 3.0

# Design rules (PCBWay standard service allows 0.127 mm; we keep margin).
MIN_CLEARANCE = 0.25
POUR_CLEARANCE = 0.4
EDGE_CLEARANCE = 0.5
MIN_ANNULAR = 0.25
SILK_W = 0.15
MASK_EXPANSION = 0.05

W_SIGNAL = 0.4
W_POWER = 0.8
W_CONTACT = 1.5  # relay contacts

FONT = FontProperties(family="DejaVu Sans", weight="bold")


# --------------------------------------------------------------------------
# Model
# --------------------------------------------------------------------------

class Pad:
    def __init__(self, ref, num, x, y, net, shape="circle", w=1.6, h=None, drill=0.8):
        self.ref, self.num = ref, str(num)
        self.x, self.y = x, y
        self.net = net
        self.shape = shape
        self.w = w
        self.h = h if h is not None else w
        self.drill = drill

    def geom(self, expand=0.0):
        if self.shape == "circle":
            return Point(self.x, self.y).buffer(self.w / 2 + expand, 32)
        if self.shape == "rect":
            return box(self.x - self.w / 2 - expand, self.y - self.h / 2 - expand,
                       self.x + self.w / 2 + expand, self.y + self.h / 2 + expand)
        raise ValueError(self.shape)

    def __repr__(self):
        return f"{self.ref}.{self.num}"


class Part:
    def __init__(self, ref, value, desc, footprint):
        self.ref, self.value, self.desc, self.footprint = ref, value, desc, footprint
        self.pads = []
        self.silk = []  # shapely geometries (already with width)
        self.label = None  # (x, y, size, anchor)

    def pad(self, num, x, y, net, **kw):
        p = Pad(self.ref, num, x, y, net, **kw)
        self.pads.append(p)
        return p


def line(*pts, w=SILK_W):
    return LineString(pts).buffer(w / 2, 8)


def ring(cx, cy, r, w=SILK_W):
    return Point(cx, cy).buffer(r + w / 2, 64).difference(Point(cx, cy).buffer(r - w / 2, 64))


def rect_outline(x0, y0, x1, y1, w=SILK_W):
    return line((x0, y0), (x1, y0), (x1, y1), (x0, y1), (x0, y0), w=w)


def arc(cx, cy, r, a0, a1, w=SILK_W, n=48):
    pts = [(cx + r * math.cos(math.radians(a0 + (a1 - a0) * i / n)),
            cy + r * math.sin(math.radians(a0 + (a1 - a0) * i / n))) for i in range(n + 1)]
    return line(*pts, w=w)


def text(s, x, y, size=1.2, anchor="left", angle=0):
    """Text as filled polygons. size is the cap height in mm."""
    tp = TextPath((0, 0), s, size=size / 0.73, prop=FONT)
    rings = [r for r in tp.to_polygons() if len(r) >= 3]
    shape = Polygon()
    for r in rings:  # even-odd fill
        shape = shape.symmetric_difference(Polygon(r).buffer(0))
    minx, _, maxx, _ = shape.bounds
    dx = {"left": -minx, "center": -(minx + maxx) / 2, "right": -maxx}[anchor]
    out = []
    geoms = shape.geoms if hasattr(shape, "geoms") else [shape]
    for g in geoms:
        def tf(c):
            res = []
            for px, py in c:
                px, py = px + dx, py - size / 2  # vertically centred on y
                if angle:
                    a = math.radians(angle)
                    px, py = px * math.cos(a) - py * math.sin(a), px * math.sin(a) + py * math.cos(a)
                res.append((x + px, y - py))
            return res
        out.append(Polygon(tf(g.exterior.coords), [tf(i.coords) for i in g.interiors]))
    return unary_union(out)


# --------------------------------------------------------------------------
# Footprints
# --------------------------------------------------------------------------

def axial(ref, value, desc, p1, p2, net1, net2, body_len=6.3, body_d=2.5, pad=1.6, drill=0.8,
          diode=False, label_side=1, fp="R_Axial_DIN0207_P10.16mm"):
    """Axial part. For diodes pad 1 (square) is the cathode."""
    part = Part(ref, value, desc, fp)
    part.pad(1, *p1, net1, shape="rect" if diode else "circle", w=pad, drill=drill)
    part.pad(2, *p2, net2, w=pad, drill=drill)
    (x1, y1), (x2, y2) = p1, p2
    length = math.hypot(x2 - x1, y2 - y1)
    ux, uy = (x2 - x1) / length, (y2 - y1) / length
    vx, vy = -uy, ux
    mx, my = (x1 + x2) / 2, (y1 + y2) / 2
    hl, hd = body_len / 2, body_d / 2
    corners = [(mx + ux * a * hl + vx * b * hd, my + uy * a * hl + vy * b * hd)
               for a, b in ((-1, -1), (1, -1), (1, 1), (-1, 1), (-1, -1))]
    part.silk.append(line(*corners))
    part.silk.append(line((x1 + ux * (pad / 2 + 0.3), y1 + uy * (pad / 2 + 0.3)), (mx - ux * hl, my - uy * hl)))
    part.silk.append(line((x2 - ux * (pad / 2 + 0.3), y2 - uy * (pad / 2 + 0.3)), (mx + ux * hl, my + uy * hl)))
    if diode:  # cathode band
        bx, by = mx - ux * (hl - 0.8), my - uy * (hl - 0.8)
        part.silk.append(line((bx + vx * hd, by + vy * hd), (bx - vx * hd, by - vy * hd), w=0.5))
    off = hd + 1.0
    angle = 0 if abs(uy) < 0.5 else 90
    part.label = (mx + vx * off * label_side, my + vy * off * label_side, 1.0, "center", angle)
    return part


def to92(ref, value, desc, e, b, c, nets, flat_up=True):
    """TO-92 (BC547), pins E-B-C in a row, 2.54 mm pitch."""
    part = Part(ref, value, desc, "TO-92_Inline_Wide")
    for num, (pos, net) in zip(("E", "B", "C"), zip((e, b, c), nets)):
        part.pad(num, *pos, net, shape="rect" if num == "E" else "circle", w=1.5, drill=0.75)
    cx, cy = b
    r, d = 2.5, 1.7
    sgn = -1 if flat_up else 1
    half = math.degrees(math.asin(d / r))
    # flat face towards sgn*y, round back on the other side
    if flat_up:
        part.silk.append(arc(cx, cy, r, -half, 180 + half))
    else:
        part.silk.append(arc(cx, cy, r, 180 - half, 360 + half))
    dx = math.sqrt(r * r - d * d)
    part.silk.append(line((cx - dx, cy + sgn * d), (cx + dx, cy + sgn * d)))
    part.label = (cx, cy - sgn * (r + 1.0), 1.0, "center", 0)
    return part


def led5(ref, value, desc, a, k, net_a, net_k):
    part = Part(ref, value, desc, "LED_D5.0mm")
    part.pad(1, *k, net_k, shape="rect", w=1.8, drill=0.9)  # K
    part.pad(2, *a, net_a, w=1.8, drill=0.9)  # A
    cx, cy = (a[0] + k[0]) / 2, (a[1] + k[1]) / 2
    ux, uy = (k[0] - a[0]) / 2.54, (k[1] - a[1]) / 2.54
    base = math.degrees(math.atan2(uy, ux))
    r, d = 2.9, 2.4
    half = math.degrees(math.acos(d / r))
    part.silk.append(arc(cx, cy, r, base + half, base + 360 - half))
    p = lambda ang: (cx + r * math.cos(math.radians(ang)), cy + r * math.sin(math.radians(ang)))
    part.silk.append(line(p(base - half), p(base + half)))
    part.label = (cx - uy * 4.2, cy + ux * 4.2, 1.0, "center", 0)
    return part


def cap_radial(ref, value, desc, plus, minus, net_p, net_m, d=6.3):
    part = Part(ref, value, desc, f"CP_Radial_D{d}mm_P2.50mm")
    part.pad(1, *plus, net_p, shape="rect", w=1.6, drill=0.8)
    part.pad(2, *minus, net_m, w=1.6, drill=0.8)
    cx, cy = (plus[0] + minus[0]) / 2, (plus[1] + minus[1]) / 2
    part.silk.append(ring(cx, cy, d / 2 + 0.15))
    ux, uy = plus[0] - minus[0], plus[1] - minus[1]
    n = math.hypot(ux, uy)
    ux, uy = ux / n, uy / n
    px, py = plus[0] + ux * 0.2 - uy * 2.0, plus[1] + uy * 0.2 + ux * 2.0
    part.silk += [line((px - 0.6, py), (px + 0.6, py)), line((px, py - 0.6), (px, py + 0.6))]
    part.label = (cx, cy - d / 2 - 1.2, 1.0, "center", 0)
    return part


def buzzer(ref, value, desc, plus, minus, net_p, net_m):
    part = Part(ref, value, desc, "Buzzer_12x9.5RM7.6")
    part.pad(1, *plus, net_p, shape="rect", w=2.0, drill=1.0)
    part.pad(2, *minus, net_m, w=2.0, drill=1.0)
    cx, cy = (plus[0] + minus[0]) / 2, (plus[1] + minus[1]) / 2
    part.silk.append(ring(cx, cy, 6.1))
    px, py = plus[0], plus[1] - 2.3
    part.silk += [line((px - 0.6, py), (px + 0.6, py)), line((px, py - 0.6), (px, py + 0.6))]
    part.label = (cx, cy + 3.0, 1.0, "center", 0)
    return part


def dip4(ref, value, desc, pin1, nets):
    """DIP-4 (PC817): 1 A, 2 K on the left column, 3 E, 4 C on the right."""
    part = Part(ref, value, desc, "DIP-4_W7.62mm")
    x, y = pin1
    pos = {1: (x, y), 2: (x, y + 2.54), 3: (x + 7.62, y + 2.54), 4: (x + 7.62, y)}
    for num in (1, 2, 3, 4):
        part.pad(num, *pos[num], nets[num - 1], shape="rect" if num == 1 else "circle", w=1.6, drill=0.8)
    x0, x1 = x + 1.2, x + 7.62 - 1.2
    y0, y1 = y - 1.9, y + 2.54 + 1.9
    mx = (x0 + x1) / 2
    part.silk.append(line((mx - 0.9, y0), (x0, y0), (x0, y1), (x1, y1), (x1, y0), (mx + 0.9, y0)))
    part.silk.append(arc(mx, y0, 0.9, 0, 180))
    part.label = (mx, y1 + 1.2, 1.0, "center", 0)
    return part


def sip3(ref, value, desc, pin1, nets):
    """Switching regulator, 7805 pinout (1 VIN, 2 GND, 3 VOUT), SIP-3 2.54 mm."""
    part = Part(ref, value, desc, "SIP-3_P2.54mm")
    x, y = pin1
    for i in range(3):
        part.pad(i + 1, x + 2.54 * i, y, nets[i], shape="rect" if i == 0 else "circle", w=1.8, drill=1.0)
    part.silk.append(rect_outline(x - 3.2, y - 2.0, x + 5.08 + 3.2, y + 2.6))
    part.silk.append(line((x - 3.2, y + 1.6), (x + 8.28, y + 1.6)))
    part.label = (x + 2.54, y - 3.0, 1.0, "center", 0)
    return part


def terminal(ref, value, desc, x, ys, nets, labels=None):
    """Screw terminal, 5.08 mm, pins in a vertical column, wire entry to the right edge."""
    part = Part(ref, value, desc, f"TerminalBlock_5.08mm_1x{len(ys):02d}")
    for i, (y, net) in enumerate(zip(ys, nets)):
        part.pad(i + 1, x, y, net, shape="rect" if i == 0 else "circle", w=2.6, drill=1.3)
    y0, y1 = min(ys) - 2.54, max(ys) + 2.54
    part.silk.append(rect_outline(x - 5.0, y0, x + 4.6, y1))
    part.silk.append(line((x - 3.6, y0), (x - 3.6, y1)))
    part.label = (x, y0 - 1.2, 1.0, "center", 0)
    return part


def header(ref, value, desc, x, y0, n, nets, female=True, horizontal=False, fp=None):
    part = Part(ref, value, desc, fp or (f"PinSocket_1x{n:02d}_P2.54mm" if female else f"PinHeader_1x{n:02d}_P2.54mm"))
    for i in range(n):
        px, py = (x + 2.54 * i, y0) if horizontal else (x, y0 + 2.54 * i)
        part.pad(i + 1, px, py, nets[i], shape="rect" if i == 0 else "circle", w=1.7, drill=1.0)
    if horizontal:
        part.silk.append(rect_outline(x - 1.27, y0 - 1.27, x + 2.54 * (n - 1) + 1.27, y0 + 1.27))
    else:
        part.silk.append(rect_outline(x - 1.27, y0 - 1.27, x + 1.27, y0 + 2.54 * (n - 1) + 1.27))
    return part


def relay(ref, value, desc, com, nets):
    """Songle SRD-05VDC-SL-C. com = COM pin; coils 2 mm right of it at +-6 mm,
    contacts 14.2 mm right at +-6 mm (NO up, NC down - check the part)."""
    part = Part(ref, value, desc, "Relay_SPDT_SANYOU_SRD_Series_Form_C")
    x, y = com
    n_com, n_coil_up, n_coil_dn, n_no, n_nc = nets
    part.pad("COM", x, y, n_com, shape="rect", w=2.6, drill=1.3)
    part.pad("C1", x + 2.0, y - 6.0, n_coil_up, w=2.6, drill=1.3)
    part.pad("C2", x + 2.0, y + 6.0, n_coil_dn, w=2.6, drill=1.3)
    part.pad("NO", x + 14.2, y - 6.0, n_no, w=2.6, drill=1.3)
    part.pad("NC", x + 14.2, y + 6.0, n_nc, w=2.6, drill=1.3)
    part.silk.append(rect_outline(x - 1.6, y - 7.75, x + 17.4, y + 7.75))
    part.label = (x + 8.0, y - 3.0, 1.0, "center", 0)
    return part


# --------------------------------------------------------------------------
# Board: placement and nets
# --------------------------------------------------------------------------

def build():
    parts, tracks, holes = [], [], []

    def track(layer, net, width, *pts):
        tracks.append((layer, net, width, list(pts)))

    # ESP32 DevKit V1 (DOIT, 30 pins): two 1x15 sockets 25.4 mm apart, antenna
    # end at the top (overhangs the board edge), USB at the bottom.
    left = ["EN", "VP", "VN", "D34", "D35", "D32", "D33", "D25", "D26", "D27", "D14", "D12", "D13", "GND", "VIN"]
    right = ["D23", "D22", "TX0", "RX0", "D21", "D19", "D18", "D5", "TX2", "RX2", "D4", "D2", "D15", "GND", "3V3"]
    used = {"GND": "GND", "VIN": "+5V", "D23": "D23", "D5": "D5", "D4": "D4", "D2": "D2"}
    LX, RX, PY = 8.0, 33.4, 6.0
    py = lambda i: PY + 2.54 * i
    u1l = header("U1A", "Soquete ESP32", "Barra femea 1x15 2,54 mm", LX, PY, 15,
                 [used.get(n, f"nc_L_{n}") for n in left])
    u1r = header("U1B", "Soquete ESP32", "Barra femea 1x15 2,54 mm", RX, PY, 15,
                 [used.get(n, f"nc_R_{n}") for n in right])
    parts += [u1l, u1r]

    # Power: VIN 7-24 V -> D1 (reverse polarity) -> PS1 (switching 5 V) -> +5V
    j1 = terminal("J1", "VIN 7-24V", "Borne KF301/KF128 2 vias 5,08 mm", 5.5, [52.0, 57.08], ["VIN", "GND"])
    d1 = axial("D1", "1N5819", "Diodo Schottky 1A 40V DO-41", (20.16, 52.0), (10.0, 52.0), "VIN_P", "VIN",
               body_len=5.2, body_d=2.7, pad=2.0, drill=1.0, diode=True, fp="D_DO-41_P10.16mm", label_side=-1)
    c1 = cap_radial("C1", "47uF 35V", "Capacitor eletrolitico 47 uF 35 V 6,3 mm", (12.5, 61.0), (10.0, 61.0), "VIN_P", "GND")
    ps1 = sip3("PS1", "K7805-1000R3", "Regulador chaveado 5 V 1 A pino 7805 (K7805-1000R3 / R-78E5.0-1.0)",
               (30.0, 56.0), ["VIN_P", "GND", "+5V"])
    c2 = cap_radial("C2", "100uF 16V", "Capacitor eletrolitico 100 uF 16 V 6,3 mm", (40.0, 61.0), (42.5, 61.0), "+5V", "GND")
    parts += [j1, d1, c1, ps1, c2]
    track("F", "VIN", W_POWER, (5.5, 52.0), (10.0, 52.0))
    track("F", "VIN_P", W_POWER, (20.16, 52.0), (29.0, 52.0), (30.0, 53.0), (30.0, 56.0))
    track("F", "VIN_P", W_POWER, (12.5, 61.0), (18.0, 61.0), (20.16, 58.84), (20.16, 52.0))
    track("F", "+5V", W_POWER, (35.08, 56.0), (35.08, 49.0), (10.0, 49.0), (8.0, 47.0), (8.0, py(14)))
    track("F", "+5V", W_POWER, (35.08, 56.0), (40.0, 60.92), (40.0, 61.0))
    track("B", "+5V", W_POWER, (35.08, 56.0), (66.62, 56.0), (73.62, 49.0))

    # Relay: D23 -> R1 -> Q1 (BC547) -> coil; D2 flyback; R2 base pull-down
    r1 = axial("R1", "1k", "Resistor 1 kOhm 1/4 W", (40.0, 6.0), (50.16, 6.0), "D23", "Q1_B")
    r2 = axial("R2", "10k", "Resistor 10 kOhm 1/4 W", (53.0, 9.0), (53.0, 19.16), "Q1_B", "GND", label_side=-1)
    q1 = to92("Q1", "BC547", "Transistor NPN BC547 TO-92", (56.0, 5.0), (58.54, 5.0), (61.08, 5.0),
              ["GND", "Q1_B", "COIL"])
    q1.label = (55.0, 9.4, 1.0, "center", 0)
    k1 = relay("K1", "SRD-05VDC-SL-C", "Rele 5 V 10 A SPDT Songle SRD-05VDC-SL-C", (62.0, 18.0),
               ["RL_COM", "COIL", "+5V", "RL_NO", "RL_NC"])
    d2 = axial("D2", "1N4148", "Diodo 1N4148 DO-35", (58.5, 24.0), (58.5, 12.0), "+5V", "COIL",
               body_len=3.8, body_d=1.8, diode=True, fp="D_DO-35_P12.00mm", label_side=1)
    d2.label = (56.6, 18.0, 1.0, "center", 90)
    j2 = terminal("J2", "RELE", "Borne KF301/KF128 3 vias 5,08 mm", 94.5, [12.92, 18.0, 23.08],
                  ["RL_NO", "RL_COM", "RL_NC"])
    parts += [r1, r2, q1, k1, d2, j2]
    track("F", "D23", W_SIGNAL, (RX, py(0)), (40.0, 6.0))
    track("F", "Q1_B", W_SIGNAL, (50.16, 6.0), (50.16, 9.0), (58.54, 9.0), (58.54, 5.0))
    track("F", "COIL", W_POWER, (61.08, 5.0), (64.0, 7.92), (64.0, 12.0))
    track("F", "COIL", W_SIGNAL, (58.5, 12.0), (64.0, 12.0))
    track("F", "+5V", W_SIGNAL, (58.5, 24.0), (64.0, 24.0))
    track("F", "RL_COM", W_CONTACT, (62.0, 18.0), (94.5, 18.0))
    track("F", "RL_NO", W_CONTACT, (76.2, 12.0), (77.12, 12.92), (94.5, 12.92))
    track("F", "RL_NC", W_CONTACT, (76.2, 24.0), (77.12, 23.08), (94.5, 23.08))
    track("B", "+5V", W_POWER, (73.6, 40.0), (73.6, 30.0), (64.0, 30.0), (64.0, 24.0))

    # Gate output: D5 -> R5 -> PC817 (isolated output, dry contact) and J4 header
    r5 = axial("R5", "330", "Resistor 330 Ohm 1/4 W", (40.0, 23.78), (50.16, 23.78), "D5", "OPTO_A", label_side=-1)
    u2 = dip4("U2", "PC817", "Optoacoplador PC817 DIP-4", (53.0, 27.0), ["OPTO_A", "GND", "GATE_E", "GATE_C"])
    j3 = terminal("J3", "PORTAO", "Borne KF301/KF128 2 vias 5,08 mm", 94.5, [30.0, 35.08], ["GATE_C", "GATE_E"])
    j4 = header("J4", "D5/GND", "Barra de pinos macho 1x2 2,54 mm", 40.0, 19.5, 2, ["D5", "GND"],
                female=False, horizontal=True)
    parts += [r5, u2, j3, j4]
    track("F", "D5", W_SIGNAL, (RX, py(7)), (40.0, 23.78))
    track("F", "D5", W_SIGNAL, (40.0, 23.78), (40.0, 19.5))
    track("F", "OPTO_A", W_SIGNAL, (50.16, 23.78), (53.0, 26.62), (53.0, 27.0))
    track("F", "GATE_C", W_SIGNAL, (60.62, 27.0), (88.0, 27.0), (91.0, 30.0), (94.5, 30.0))
    track("F", "GATE_E", W_SIGNAL, (60.62, 29.54), (60.62, 33.0), (88.0, 33.0), (90.08, 35.08), (94.5, 35.08))

    # Buzzer: D4 -> R3 -> Q2 (BC547) -> passive buzzer; D3 flyback; R4 pull-down
    r3 = axial("R3", "1k", "Resistor 1 kOhm 1/4 W", (40.0, 31.4), (50.16, 31.4), "D4", "Q2_B", label_side=-1)
    q2 = to92("Q2", "BC547", "Transistor NPN BC547 TO-92", (50.0, 37.0), (52.54, 37.0), (55.08, 37.0),
              ["GND", "Q2_B", "BZ_N"])
    q2.label = (46.6, 37.0, 1.0, "center", 0)
    r4 = axial("R4", "10k", "Resistor 10 kOhm 1/4 W", (52.54, 41.5), (52.54, 51.66), "Q2_B", "GND")
    bz1 = buzzer("BZ1", "Buzzer passivo", "Buzzer passivo 12 mm 5 V (passo 7,6 mm)", (73.6, 40.0), (66.0, 40.0),
                 "+5V", "BZ_N")
    d3 = axial("D3", "1N4148", "Diodo 1N4148 DO-35", (73.62, 49.0), (66.0, 49.0), "+5V", "BZ_N",
               body_len=3.8, body_d=1.8, diode=True, fp="D_DO-35_P7.62mm")
    parts += [r3, q2, r4, bz1, d3]
    track("F", "D4", W_SIGNAL, (RX, py(10)), (40.0, 31.4))
    track("F", "Q2_B", W_SIGNAL, (50.16, 31.4), (52.54, 33.78), (52.54, 37.0), (52.54, 41.5))
    track("F", "BZ_N", W_POWER, (55.08, 37.0), (58.08, 40.0), (66.0, 40.0), (66.0, 49.0))
    track("F", "+5V", W_POWER, (73.6, 40.0), (73.62, 49.0))

    # Status LED on D2 (mirrors the onboard LED) and power LED
    r6 = axial("R6", "330", "Resistor 330 Ohm 1/4 W", (38.0, 44.0), (48.16, 44.0), "D2", "LED2_A", label_side=-1)
    led2 = led5("LED2", "Vermelho", "LED 5 mm vermelho (alerta)", (48.16, 47.46), (48.16, 50.0), "LED2_A", "GND")
    led2.label = (44.6, 48.7, 1.0, "center", 0)
    r8 = axial("R7", "1k", "Resistor 1 kOhm 1/4 W", (47.0, 64.0), (57.16, 64.0), "+5V", "LED1_A")
    led1 = led5("LED1", "Verde", "LED 5 mm verde (energia)", (60.0, 64.0), (62.54, 64.0), "LED1_A", "GND")
    led1.label = (61.27, 59.8, 1.0, "center", 0)
    parts += [r6, led2, r8, led1]
    track("F", "D2", W_SIGNAL, (RX, py(11)), (36.0, 36.54), (36.0, 44.0), (38.0, 44.0))
    track("F", "LED2_A", W_SIGNAL, (48.16, 44.0), (48.16, 47.46))
    track("B", "+5V", W_POWER, (47.0, 56.0), (47.0, 64.0))
    track("F", "LED1_A", W_SIGNAL, (57.16, 64.0), (60.0, 64.0))

    # Mounting holes (M3)
    holes += [(4.0, 66.0, 3.2), (96.0, 66.0, 3.2), (96.0, 4.0, 3.2)]

    # Free silkscreen
    silk = []
    for i, n in enumerate(left):
        silk.append(text(n, LX + 1.6, py(i), 0.8, "left"))
    for i, n in enumerate(right):
        silk.append(text(n, RX - 1.6, py(i), 0.8, "right"))
    silk.append(rect_outline(LX - 1.3 - 0.6, -0.0 + 0.6, RX + 1.3 + 0.6, py(14) + 3.9))
    silk.append(text("ESP32 DevKit V1 (30 pinos)", (LX + RX) / 2, 2.3, 1.0, "center"))
    silk.append(text("ANTENA PARA FORA", (LX + RX) / 2, 4.2, 0.8, "center"))
    silk.append(text("USB", (LX + RX) / 2, py(14) + 2.2, 1.0, "center"))
    silk.append(text("+", 1.2, 52.0, 1.4, "center"))
    silk.append(text("-", 1.2, 57.08, 1.4, "center"))
    silk.append(text("VIN 7-24V", 1.0, 46.0, 1.0, "left"))
    for y, s in ((12.92, "NA"), (18.0, "COM"), (23.08, "NF")):
        silk.append(text(s, 88.6, y - 1.6, 0.9, "right"))
    silk.append(text("RELE", 86.0, 8.6, 1.0, "center"))
    silk.append(text("C", 88.6, 28.8, 0.9, "right"))
    silk.append(text("E", 88.6, 34.6, 0.9, "right"))
    silk.append(text("PORTAO (opto)", 94.5, 39.4, 0.9, "center"))
    silk.append(text("D5", 40.0, 17.3, 0.8, "center"))
    silk.append(text("GND", 42.54, 17.3, 0.8, "center"))
    silk.append(text("pingzap", 80.0, 56.0, 3.2, "center"))
    silk.append(text("ESP32 gate shield v1.0", 80.0, 60.6, 1.1, "center"))
    silk.append(text("PULSE: D23 rele + D5 opto | ON/OFF: D4 buzzer + D2 LED", 63.0, 67.6, 0.8, "left"))

    return parts, tracks, holes, silk


# --------------------------------------------------------------------------
# Geometry, pour, checks
# --------------------------------------------------------------------------

def board_outline():
    r = CORNER_R
    return box(r, r, BOARD_W - r, BOARD_H - r).buffer(r, 32)


def track_geom(width, pts):
    return LineString(pts).buffer(width / 2, 16) if len(pts) > 1 else Point(pts[0]).buffer(width / 2, 16)


def copper_features(parts, tracks):
    """(layer, net, geom, label) for every pad (both layers) and track."""
    feats = []
    for part in parts:
        for p in part.pads:
            for layer in ("F", "B"):
                feats.append((layer, p.net, p.geom(), repr(p)))
    for i, (layer, net, width, pts) in enumerate(tracks):
        feats.append((layer, net, track_geom(width, pts), f"track{i}:{net}"))
    return feats


def ground_pour(layer, feats, parts, holes):
    area = board_outline().buffer(-EDGE_CLEARANCE)
    others = unary_union([g.buffer(POUR_CLEARANCE, 16) for l, n, g, _ in feats if l == layer and n != "GND"])
    holes_g = unary_union([Point(x, y).buffer(d / 2 + EDGE_CLEARANCE, 32) for x, y, d in holes])
    pour = area.difference(others).difference(holes_g)
    gnd_pads = [p for part in parts for p in part.pads if p.net == "GND"]
    # Thermal reliefs: clearance ring around each GND pad, then 4 spokes.
    for p in gnd_pads:
        pad = p.geom()
        pour = pour.difference(p.geom(0.35).difference(pad))
    spokes = []
    for p in gnd_pads:
        reach = max(p.w, p.h) / 2 + 0.9
        for dx, dy in ((1, 0), (-1, 0), (0, 1), (0, -1)):
            spokes.append(LineString([(p.x, p.y), (p.x + dx * reach, p.y + dy * reach)]).buffer(0.25, cap_style=2))
    spokes = unary_union(spokes).difference(others).intersection(area)
    pour = unary_union([pour, spokes] + [p.geom() for p in gnd_pads]).intersection(area).difference(others)
    # Drop islands that touch no GND pad.
    gnd_union = unary_union([p.geom() for p in gnd_pads] +
                            [g for l, n, g, _ in feats if l == layer and n == "GND"])
    polys = pour.geoms if isinstance(pour, MultiPolygon) else [pour]
    keep = [g for g in polys if g.area > 0.5 and g.intersects(gnd_union)]
    return unary_union(keep)


def drc(parts, tracks, holes, feats, pours):
    errors = []
    min_seen = {}
    by_layer = defaultdict(list)
    for f in feats:
        by_layer[f[0]].append(f)
    for layer in ("F", "B"):
        fl = by_layer[layer] + [(layer, "GND", pours[layer], f"pour{layer}")]
        for i in range(len(fl)):
            for j in range(i + 1, len(fl)):
                a, b = fl[i], fl[j]
                if a[1] == b[1]:
                    continue
                d = a[2].distance(b[2])
                need = POUR_CLEARANCE - 0.01 if "pour" in a[3] + b[3] else MIN_CLEARANCE
                key = "pour" if "pour" in a[3] + b[3] else "copper"
                min_seen[key] = min(min_seen.get(key, 99), d)
                if d < need:
                    errors.append(f"[{layer}] clearance {d:.3f} < {need} between {a[3]} ({a[1]}) and {b[3]} ({b[1]})")
    outline = board_outline()
    edge = outline.boundary
    for layer, net, g, label in feats:
        if not outline.contains(g) or g.distance(edge) < EDGE_CLEARANCE - 0.01:
            errors.append(f"[{layer}] {label} too close to the board edge ({g.distance(edge):.2f})")
    all_holes = [(p.x, p.y, p.drill, repr(p)) for part in parts for p in part.pads] + \
                [(x, y, d, f"hole@{x},{y}") for x, y, d in holes]
    for i in range(len(all_holes)):
        for j in range(i + 1, len(all_holes)):
            a, b = all_holes[i], all_holes[j]
            gap = math.hypot(a[0] - b[0], a[1] - b[1]) - a[2] / 2 - b[2] / 2
            if gap < 0.5:
                errors.append(f"hole gap {gap:.2f} between {a[3]} and {b[3]}")
    for part in parts:
        for p in part.pads:
            ann = min(p.w, p.h) / 2 - p.drill / 2
            if ann < MIN_ANNULAR:
                errors.append(f"annular ring {ann:.2f} on {p!r}")
    for x, y, d in holes:
        for layer, net, g, label in feats:
            if g.distance(Point(x, y)) < d / 2 + 0.5:
                errors.append(f"{label} too close to mounting hole at {x},{y}")
    return errors, min_seen


def connectivity(parts, feats, pours):
    """Every net must be a single connected piece of copper (THT pads join layers)."""
    errors = []
    nets = defaultdict(list)
    for layer, net, g, label in feats:
        nets[net].append((layer, g, label))
    for layer in ("F", "B"):
        nets["GND"].append((layer, pours[layer], f"pour{layer}"))
    for net, items in nets.items():
        if net.startswith("nc_"):
            continue
        n = len(items)
        parent = list(range(n))

        def find(i):
            while parent[i] != i:
                parent[i] = parent[parent[i]]
                i = parent[i]
            return i

        for i in range(n):
            for j in range(i + 1, n):
                li, gi, ai = items[i]
                lj, gj, aj = items[j]
                same_pad = ai == aj  # THT pad on both layers
                if (li == lj and gi.intersects(gj)) or same_pad:
                    parent[find(i)] = find(j)
        groups = defaultdict(list)
        for i in range(n):
            groups[find(i)].append(items[i][2])
        if len(groups) > 1:
            errors.append(f"net {net} split in {len(groups)} pieces: " +
                          " | ".join(", ".join(sorted(set(g))[:6]) for g in groups.values()))
    return errors


# --------------------------------------------------------------------------
# Output
# --------------------------------------------------------------------------

def fmt(v):
    return str(int(round(v * 1_000_000)))


def gx(x):
    return fmt(x)


def gy(y):
    return fmt(BOARD_H - y)


class Gerber:
    def __init__(self, function, polarity="Positive"):
        self.lines = ["%TF.GenerationSoftware,pingzap,gen_pcb.py,1.0*%",
                      f"%TF.FileFunction,{function}*%",
                      f"%TF.FilePolarity,{polarity}*%",
                      "%FSLAX46Y46*%", "%MOMM*%", "%LPD*%", "G01*"]
        self.apertures = {}
        self.body = []

    def aperture(self, kind, *dims):
        key = (kind,) + tuple(round(d, 4) for d in dims)
        if key not in self.apertures:
            code = 10 + len(self.apertures)
            self.apertures[key] = code
        return self.apertures[key]

    def flash(self, kind, dims, x, y):
        code = self.aperture(kind, *dims)
        self.body.append(f"D{code}*")
        self.body.append(f"X{gx(x)}Y{gy(y)}D03*")

    def stroke(self, width, pts):
        code = self.aperture("C", width)
        self.body.append(f"D{code}*")
        self.body.append(f"X{gx(pts[0][0])}Y{gy(pts[0][1])}D02*")
        for x, y in pts[1:]:
            self.body.append(f"X{gx(x)}Y{gy(y)}D01*")

    def region(self, coords):
        coords = list(coords)
        self.body.append("G36*")
        self.body.append(f"X{gx(coords[0][0])}Y{gy(coords[0][1])}D02*")
        for x, y in coords[1:]:
            self.body.append(f"X{gx(x)}Y{gy(y)}D01*")
        self.body.append("G37*")

    def polygons(self, geom):
        """Filled shapely geometry as dark regions only: polygons with holes are
        cut into hole-free pieces, so no clear polarity is needed."""
        polys = geom.geoms if hasattr(geom, "geoms") else [geom]
        for p in polys:
            for piece in split_holes(p):
                self.region(piece.exterior.coords)

    def save(self, path):
        ap = []
        for key, code in sorted(self.apertures.items(), key=lambda kv: kv[1]):
            kind, dims = key[0], key[1:]
            ap.append(f"%ADD{code}{kind},{'X'.join(f'{d:.4f}' for d in dims)}*%")
        with open(path, "w") as f:
            f.write("\n".join(self.lines + ap + self.body + ["M02*"]) + "\n")


def split_holes(poly):
    """Cuts a polygon with holes into hole-free polygons (vertical cuts through holes)."""
    if poly.is_empty or poly.geom_type != "Polygon":
        return []
    if not poly.interiors:
        return [poly]
    x = poly.interiors[0].centroid.x
    _, miny, _, maxy = poly.bounds
    pieces = split(poly, LineString([(x, miny - 1), (x, maxy + 1)]))
    out = []
    for piece in pieces.geoms:
        out += split_holes(piece)
    return out


def flash_pad(g, p, expand=0.0):
    if p.shape == "circle":
        g.flash("C", (p.w + 2 * expand,), p.x, p.y)
    else:
        g.flash("R", (p.w + 2 * expand, p.h + 2 * expand), p.x, p.y)


def write_excellon(path, items, plated):
    tools = sorted({round(d, 2) for _, _, d in items})
    out = ["M48", f"; pingzap gen_pcb.py - {'PTH' if plated else 'NPTH'}", "FMAT,2", "METRIC,TZ"]
    out.append(f"; #@! TF.FileFunction,{'Plated' if plated else 'NonPlated'},1,2,{'PTH' if plated else 'NPTH'}")
    for i, d in enumerate(tools, 1):
        out.append(f"T{i}C{d:.3f}")
    out += ["%", "G90", "G05"]
    for i, d in enumerate(tools, 1):
        out.append(f"T{i}")
        for x, y, dd in items:
            if round(dd, 2) == d:
                out.append(f"X{x:.3f}Y{BOARD_H - y:.3f}")
    out.append("M30")
    with open(path, "w") as f:
        f.write("\n".join(out) + "\n")


def write_outputs(parts, tracks, holes, silk, pours):
    gdir = os.path.join(HERE, "gerber")
    os.makedirs(gdir, exist_ok=True)
    for f in os.listdir(gdir):
        os.remove(os.path.join(gdir, f))
    pads = [p for part in parts for p in part.pads]

    for layer, fname, func in (("F", "F_Cu.gtl", "Copper,L1,Top"), ("B", "B_Cu.gbl", "Copper,L2,Bot")):
        g = Gerber(func)
        g.polygons(pours[layer])
        for net_layer, net, width, pts in tracks:
            if net_layer == layer:
                g.stroke(width, pts)
        for p in pads:
            flash_pad(g, p)
        g.save(os.path.join(gdir, f"{NAME}-{fname}"))

    for fname, func in (("F_Mask.gts", "Soldermask,Top"), ("B_Mask.gbs", "Soldermask,Bot")):
        g = Gerber(func, "Negative")
        for p in pads:
            flash_pad(g, p, MASK_EXPANSION)
        g.save(os.path.join(gdir, f"{NAME}-{fname}"))

    mask_open = unary_union([p.geom(MASK_EXPANSION + 0.1) for p in pads])
    silk_geom = unary_union(silk + [s for part in parts for s in part.silk] + [
        text(part.ref, part.label[0], part.label[1], part.label[2], part.label[3], part.label[4])
        for part in parts if part.label])
    silk_geom = silk_geom.difference(mask_open).intersection(board_outline().buffer(-0.2))
    g = Gerber("Legend,Top")
    g.polygons(silk_geom)
    g.save(os.path.join(gdir, f"{NAME}-F_SilkS.gto"))

    g = Gerber("Profile,NP")
    g.stroke(0.1, list(board_outline().exterior.coords))
    g.save(os.path.join(gdir, f"{NAME}-Edge_Cuts.gm1"))

    write_excellon(os.path.join(gdir, f"{NAME}-PTH.drl"), [(p.x, p.y, p.drill) for p in pads], True)
    write_excellon(os.path.join(gdir, f"{NAME}-NPTH.drl"), holes, False)

    zpath = os.path.join(HERE, f"{NAME}-gerber.zip")
    with zipfile.ZipFile(zpath, "w", zipfile.ZIP_DEFLATED) as z:
        for f in sorted(os.listdir(gdir)):
            z.write(os.path.join(gdir, f), f)

    # BOM grouped by value/footprint; the ESP32 itself is listed once.
    rows = defaultdict(list)
    for part in parts:
        rows[(part.desc, part.value, part.footprint)].append(part.ref)
    with open(os.path.join(HERE, "bom.csv"), "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["Referencias", "Qtd", "Valor", "Descricao", "Footprint"])
        for (desc, value, fp), refs in sorted(rows.items(), key=lambda kv: kv[1][0]):
            w.writerow([" ".join(refs), len(refs), value, desc, fp])
        w.writerow(["U1", 1, "ESP32 DevKit V1 30 pinos", "Placa ESP32 DOIT DevKit V1 (encaixa em U1A/U1B)", "-"])
        w.writerow(["-", 3, "M3", "Espacador + parafuso M3 (furos de fixacao)", "-"])

    return silk_geom, zpath


def render(parts, tracks, holes, silk_geom, pours, path, side):
    import matplotlib
    matplotlib.use("Agg")
    import matplotlib.pyplot as plt
    from matplotlib.patches import PathPatch
    from matplotlib.path import Path

    def patch(geom, **kw):
        polys = geom.geoms if hasattr(geom, "geoms") else [geom]
        for p in polys:
            if p.is_empty or p.geom_type != "Polygon":
                continue
            verts, codes = [], []
            for ringc in [p.exterior] + list(p.interiors):
                c = list(ringc.coords)
                verts += c
                codes += [Path.MOVETO] + [Path.LINETO] * (len(c) - 2) + [Path.CLOSEPOLY]
            ax.add_patch(PathPatch(Path(verts, codes), **kw))

    fig, ax = plt.subplots(figsize=(BOARD_W / 8, BOARD_H / 8), dpi=200)
    ax.set_xlim(-1, BOARD_W + 1)
    ax.set_ylim(BOARD_H + 1, -1)
    ax.set_aspect("equal")
    ax.axis("off")
    fig.patch.set_facecolor("#1d2b22")
    pads = [p for part in parts for p in part.pads]
    patch(board_outline(), facecolor="#0f5132", edgecolor="#d9c27a", linewidth=0.8)
    patch(pours[side], facecolor="#1a6b40", edgecolor="none")
    patch(unary_union([track_geom(w, pts) for l, n, w, pts in tracks if l == side]),
          facecolor="#3fa36b", edgecolor="none")
    patch(unary_union([p.geom() for p in pads]), facecolor="#c9b26b", edgecolor="none")
    for p in pads:
        ax.add_patch(plt.Circle((p.x, p.y), p.drill / 2, color="#1d2b22"))
    for x, y, d in holes:
        ax.add_patch(plt.Circle((x, y), d / 2, color="#1d2b22"))
    if side == "F":
        patch(silk_geom, facecolor="#f2f2f2", edgecolor="none")
    else:
        ax.invert_xaxis()  # bottom seen from below
    fig.savefig(path, bbox_inches="tight", pad_inches=0.05, facecolor=fig.get_facecolor())
    plt.close(fig)


def main():
    parts, tracks, holes, silk = build()
    feats = copper_features(parts, tracks)
    pours = {layer: ground_pour(layer, feats, parts, holes) for layer in ("F", "B")}
    errors, min_seen = drc(parts, tracks, holes, feats, pours)
    errors += connectivity(parts, feats, pours)
    if errors:
        print(f"{len(errors)} DRC error(s):")
        for e in errors:
            print("  " + e)
        sys.exit(1)
    print(f"DRC ok - min copper clearance {min_seen['copper']:.3f} mm, pour clearance {min_seen['pour']:.3f} mm")
    silk_geom, zpath = write_outputs(parts, tracks, holes, silk, pours)
    render(parts, tracks, holes, silk_geom, pours, os.path.join(HERE, "preview-top.png"), "F")
    render(parts, tracks, holes, silk_geom, pours, os.path.join(HERE, "preview-bottom.png"), "B")
    nets = sorted({p.net for part in parts for p in part.pads if not p.net.startswith("nc_")})
    print(f"{sum(len(p.pads) for p in parts)} pads, {len(tracks)} tracks, {len(nets)} nets")
    print(f"wrote {os.path.relpath(zpath, HERE)}, bom.csv, preview-top.png, preview-bottom.png")


if __name__ == "__main__":
    main()
