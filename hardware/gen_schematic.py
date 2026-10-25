#!/usr/bin/env python3
"""
pingzap ESP32 shield - schematic (schematic.svg / schematic.pdf / schematic.png).

Same circuit as gen_pcb.py, drawn in blocks joined by net labels (+5V, GND,
D23, D5, D4, D2). Requirements: schemdraw, matplotlib.
"""

import os

import schemdraw
import schemdraw.elements as elm

HERE = os.path.dirname(os.path.abspath(__file__))
schemdraw.use("matplotlib")


def net(d, name, loc="left"):
    """Open dot with the net name: wires with the same name are connected."""
    d.add(elm.Dot(open=True).label(name, loc=loc, fontsize=11, color="#1f5fa8"))


def title(d, xy, text):
    d.add(elm.Label().at(xy).label(text, fontsize=13, halign="left", color="#333333"))


def build():
    d = schemdraw.Drawing(show=False)
    d.config(fontsize=10, unit=2.5, lw=1.3)

    # --- Power -------------------------------------------------------------
    title(d, (0, 4.2), "Alimentação: 7-24 V -> 5 V")
    d.add(elm.Dot(open=True).at((0, 2)).label("J1 +\nVIN 7-24V", loc="left"))
    d.add(elm.Diode().right().label("D1\n1N5819", loc="top"))
    vinp = d.add(elm.Dot())
    d.add(elm.Line().right(1.5))
    ps1 = d.add(elm.Ic(pins=[elm.IcPin(name="VIN", side="left", pin="1"),
                             elm.IcPin(name="GND", side="bottom", pin="2"),
                             elm.IcPin(name="5V", side="right", pin="3")],
                       size=(3.2, 1.8), lblsize=10).right().anchor("VIN").label("PS1\nK7805-1000R3", loc="top"))
    d.add(elm.Ground().at(ps1.GND))
    d.add(elm.Capacitor(polar=True).at(vinp.center).down().label("C1\n47uF 35V", loc="bottom"))
    d.add(elm.Ground())
    d.add(elm.Line().at(ps1["5V"]).right(1.5))
    v5 = d.add(elm.Dot())
    d.add(elm.Vdd().at(v5.center).label("+5V"))
    d.add(elm.Capacitor(polar=True).at(v5.center).down().label("C2\n100uF 16V", loc="bottom"))
    d.add(elm.Ground())
    d.add(elm.Line().at(v5.center).right(2))
    d.add(elm.Resistor().down().label("R7\n1k", loc="bottom"))
    d.add(elm.LED().down().label("LED1\nverde", loc="bottom"))
    d.add(elm.Ground())
    d.add(elm.Dot(open=True).at((0, -1.2)).label("J1 -", loc="left"))
    d.add(elm.Ground())

    # --- ESP32 -------------------------------------------------------------
    title(d, (0, -7.5), "ESP32 DevKit V1 (U1, encaixada em U1A/U1B)")
    esp = d.add(elm.Ic(pins=[elm.IcPin(name="VIN", side="left", pin=""),
                             elm.IcPin(name="GND", side="left", pin=""),
                             elm.IcPin(name="D23", side="right", pin=""),
                             elm.IcPin(name="D5", side="right", pin=""),
                             elm.IcPin(name="D4", side="right", pin=""),
                             elm.IcPin(name="D2", side="right", pin=""),
                             elm.IcPin(name="GND", side="right", pin="")],
                       size=(4, 5), label="ESP32\nDevKit V1", lblsize=12).right().at((3, -14)))
    d.add(elm.Line().at(esp.inL1).left(1.2))
    net(d, "+5V")
    d.add(elm.Line().at(esp.inL2).left(1.2))
    d.add(elm.Ground())
    for i, name in enumerate(("D23", "D5", "D4", "D2"), start=1):
        d.add(elm.Line().at(esp[f"inR{i}"]).right(1.2))
        net(d, name, loc="right")
    d.add(elm.Line().at(esp.inR5).right(1.2))
    d.add(elm.Ground())

    # --- Relay -------------------------------------------------------------
    x0 = 22
    title(d, (x0, 6.2), "Relé do portão (comando PULSE, D23 por 1 s)")
    d.add(elm.Dot(open=True).at((x0, -1)).label("D23", loc="left", color="#1f5fa8"))
    d.add(elm.Resistor().right().label("R1 1k"))
    b1 = d.add(elm.Dot())
    q1 = d.add(elm.BjtNpn(circle=True).anchor("base").label("Q1\nBC547", loc="right"))
    d.add(elm.Ground().at(q1.emitter))
    d.add(elm.Resistor().at(b1.center).down().label("R2\n10k", loc="top"))
    d.add(elm.Ground())
    d.add(elm.Line().at(q1.collector).up(1.2))
    coil_low = d.add(elm.Dot())
    k1 = d.add(elm.Relay(switch="spdt", unit=2.5).right().anchor("in2")
               .label("K1 SRD-05VDC-SL-C", loc="bottom", ofst=(0, -0.4)))
    d.add(elm.Line().at(k1.in1).up(0.6))
    top = d.add(elm.Dot())
    d.add(elm.Vdd().label("+5V"))
    d.add(elm.Line().at(coil_low.center).left(2))
    d.add(elm.Diode().up().toy(top.center).label("D2\n1N4148", loc="bottom"))
    d.add(elm.Line().tox(top.center))
    # contacts to J2: a = COM, b = NF (closed at rest), c = NA
    d.add(elm.Line().at(k1.a).up(0.8))
    d.add(elm.Line().right(3.2))
    d.add(elm.Dot(open=True).label("J2 COM", loc="right"))
    d.add(elm.Line().at(k1.b).down(0.8))
    d.add(elm.Line().right(2.0))
    d.add(elm.Dot(open=True).label("J2 NF", loc="right"))
    d.add(elm.Line().at(k1.c).down(1.6))
    d.add(elm.Line().right(2.8))
    d.add(elm.Dot(open=True).label("J2 NA", loc="right"))

    # --- Opto --------------------------------------------------------------
    y0 = -9
    title(d, (x0, y0 + 2.5), "Saída isolada do portão (D5, junto com o relé)")
    d.add(elm.Dot(open=True).at((x0, y0)).label("D5", loc="left", color="#1f5fa8"))
    d5n = d.add(elm.Dot())
    d.add(elm.Resistor().right().label("R5 330"))
    u2 = d.add(elm.Optocoupler().anchor("anode").label("U2 PC817", loc="top"))
    d.add(elm.Ground().at(u2.cathode))
    d.add(elm.Line().at(u2.collector).right(1.5))
    d.add(elm.Dot(open=True).label("J3 C (portão)", loc="right"))
    d.add(elm.Line().at(u2.emitter).right(1.5))
    d.add(elm.Dot(open=True).label("J3 E (portão)", loc="right"))
    d.add(elm.Line().at(d5n.center).down(1.5))
    d.add(elm.Dot(open=True).label("J4 D5 / GND\n(módulo externo)", loc="bottom"))

    # --- Buzzer ------------------------------------------------------------
    y1 = -19
    title(d, (x0, y1 + 5.5), "Alarme: buzzer (ON/OFF, D4 com tom de 2 kHz)")
    d.add(elm.Dot(open=True).at((x0, y1)).label("D4", loc="left", color="#1f5fa8"))
    d.add(elm.Resistor().right().label("R3 1k"))
    b2 = d.add(elm.Dot())
    q2 = d.add(elm.BjtNpn(circle=True).anchor("base").label("Q2\nBC547", loc="right"))
    d.add(elm.Ground().at(q2.emitter))
    d.add(elm.Resistor().at(b2.center).down().label("R4\n10k", loc="top"))
    d.add(elm.Ground())
    d.add(elm.Line().at(q2.collector).up(1.2))
    bzn = d.add(elm.Dot())
    bz = d.add(elm.Speaker().right().anchor("in2").label("BZ1 buzzer passivo", loc="right", ofst=(0.2, 0)))
    d.add(elm.Line().at(bz.in1).up(0.8))
    top2 = d.add(elm.Dot())
    d.add(elm.Vdd().label("+5V"))
    d.add(elm.Line().at(bzn.center).left(2))
    d.add(elm.Diode().up().toy(top2.center).label("D3\n1N4148", loc="bottom"))
    d.add(elm.Line().tox(top2.center))

    # --- LED ---------------------------------------------------------------
    x2 = 35
    title(d, (x2, y1 + 5.5), "LED de alerta (D2, igual ao LED da placa)")
    d.add(elm.Dot(open=True).at((x2, y1)).label("D2", loc="left", color="#1f5fa8"))
    d.add(elm.Resistor().right().label("R6 330"))
    d.add(elm.LED().right().label("LED2\nvermelho"))
    d.add(elm.Ground())

    d.add(elm.Label().at((0, -23.5)).label(
        "pingzap ESP32 gate shield v1.0  |  firmware: firmware/pingzap/pingzap.ino  |  "
        "nets com o mesmo nome estão ligadas", fontsize=10, halign="left", color="#555555"))
    return d


def main():
    d = build()
    for ext in ("svg", "pdf", "png"):
        d.save(os.path.join(HERE, f"schematic.{ext}"), dpi=160)
    print("wrote schematic.svg, schematic.pdf, schematic.png")


if __name__ == "__main__":
    main()
