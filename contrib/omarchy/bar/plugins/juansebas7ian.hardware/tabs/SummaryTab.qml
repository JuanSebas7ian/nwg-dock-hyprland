import QtQuick
import qs.Commons
import "../ui"

// Summary: every alert (click = go to its tab) and one tile per area.
Column {
  id: tab
  property var hw: null
  spacing: Style.space(10)

  readonly property var live: hw ? hw.live : null
  readonly property var ov: hw ? hw.storage.overview : null
  readonly property var bt: hw && hw.devices ? hw.devices.bluetooth : null
  readonly property var connected: bt ? bt.devices.filter(function(d) { return d.connected }) : []
  readonly property var batteries: {
    if (!hw || !hw.devices) return []
    var out = hw.devices.batteries.filter(function(b) { return b.percent !== null })
    connected.forEach(function(d) { if (d.battery !== null) out.push({ name: d.name, percent: d.battery }) })
    return out.sort(function(a, b) { return a.percent - b.percent })
  }

  Line {
    visible: !!tab.hw && tab.hw.alerts.length === 0 && !!tab.live
    text: "✓ No hardware alerts. Temperatures, pump, disks, drivers and devices are fine."
    tone: "dim"
  }
  Repeater {
    model: tab.hw ? tab.hw.alerts : []
    MouseArea {
      required property var modelData
      width: tab.width
      height: note.implicitHeight
      cursorShape: Qt.PointingHandCursor
      onClicked: tab.hw.setTab(modelData.tab)
      Notice {
        id: note
        width: parent.width
        level: modelData.level
        text: (modelData.level === "crit" ? "✖ " : "⚠ ") + modelData.title + (modelData.detail ? "\n" + modelData.detail : "")
      }
    }
  }

  Grid {
    id: grid
    width: parent.width
    columns: 2
    columnSpacing: Style.space(8)
    rowSpacing: Style.space(8)
    readonly property real cell: (width - columnSpacing) / 2

    Tile {
      width: grid.cell
      icon: "󰍛"
      value: tab.live ? Math.round(tab.hw.tctl) + "°C · " + Math.round(tab.live.cpu.load) + "%" : "…"
      caption: "CPU · " + (tab.live ? (tab.live.cpu.freqMax / 1000).toFixed(1) + " GHz max" : "")
      fraction: tab.live ? tab.hw.tctl / 95 : -1
      hot: tab.hw ? tab.hw.alertsFor("cpu").length > 0 : false
      onPicked: tab.hw.setTab("cpu")
    }
    Tile {
      width: grid.cell
      icon: String.fromCodePoint(0xF08AE)
      value: tab.hw && tab.hw.gpu ? tab.hw.gpu.temp + "°C · " + tab.hw.gpu.util + "%" : "…"
      caption: tab.hw && tab.hw.gpu ? tab.hw.gpu.name + " · VRAM " + Theme.gb(tab.hw.gpu.vramUsed) + " GB" : "GPU"
      fraction: tab.hw && tab.hw.gpu ? tab.hw.gpu.util / 100 : -1
      hot: tab.hw ? tab.hw.alertsFor("gpu").length > 0 : false
      onPicked: tab.hw.setTab("gpu")
    }
    Tile {
      width: grid.cell
      icon: "󰘚"
      value: tab.live ? Theme.gb(tab.live.mem.used) + " / " + Theme.gb(tab.live.mem.total) + " GB" : "…"
      caption: "RAM · swap " + (tab.live ? Theme.gb(tab.live.mem.swapUsed) + " GB" : "")
      fraction: tab.live ? tab.live.mem.used / tab.live.mem.total : -1
      onPicked: tab.hw.setTab("cpu")
    }
    Tile {
      width: grid.cell
      icon: "󰈐"
      value: tab.hw && tab.hw.pump ? tab.hw.pump.rpm + " RPM" : "…"
      caption: "AIO pump · CPU fans " + (tab.live ? tab.live.fans.filter(function(f) { return f.role === "cpu" }).map(function(f) { return f.rpm }).join("/") : "")
      hot: tab.hw ? tab.hw.alerts.some(function(a) { return a.id === "pump" }) : false
      onPicked: tab.hw.setTab("cpu")
    }
    Tile {
      width: grid.cell
      icon: "󰋊"
      value: tab.ov ? Theme.bytes(tab.ov.root.free) + " free" : "…"
      caption: "System disk · " + (tab.ov ? tab.ov.disks.length + " disks" : "")
      fraction: tab.ov ? tab.ov.root.used / tab.ov.root.size : -1
      hot: tab.hw ? tab.hw.alertsFor("storage").length > 0 : false
      onPicked: tab.hw.setTab("storage")
    }
    Tile {
      width: grid.cell
      icon: ""
      value: !tab.hw || !tab.hw.drivers ? "…" : tab.hw.updateCount > 0 ? tab.hw.updateCount + " to update" : "Up to date"
      caption: tab.hw && tab.hw.drivers
        ? "Drivers · hwcheck " + ((tab.hw.drivers.health.counts.fail || 0) + (tab.hw.drivers.health.counts.warn || 0) === 0 ? "OK" : "issues")
          + (tab.hw.drivers.bios.newer ? " · BIOS " + tab.hw.drivers.bios.version : "")
        : "Drivers"
      hot: tab.hw ? tab.hw.alertsFor("drivers").length > 0 : false
      onPicked: tab.hw.setTab("drivers")
    }
    Tile {
      width: grid.cell
      icon: !tab.bt || !tab.bt.adapter ? "󰂲" : tab.connected.length > 0 ? "󰂱" : "󰂯"
      value: !tab.bt ? "…" : !tab.bt.adapter ? "No adapter" : !tab.bt.adapter.powered ? "Off" : tab.connected.length + " connected"
      caption: tab.connected.length > 0 ? tab.connected.map(function(d) { return d.name }).join(", ") : "Bluetooth"
      hot: !!tab.bt && !tab.bt.adapter && !tab.bt.blocked
      onPicked: tab.hw.setTab("devices")
    }
    Tile {
      width: grid.cell
      icon: "󰁹"
      value: tab.batteries.length > 0 ? tab.batteries[0].percent + "%" : "–"
      caption: tab.batteries.length > 0 ? "Lowest: " + tab.batteries[0].name : "Wireless batteries"
      fraction: tab.batteries.length > 0 ? tab.batteries[0].percent / 100 : -1
      hot: tab.hw ? tab.hw.alertsFor("devices").length > 0 : false
      onPicked: tab.hw.setTab("devices")
    }
  }

  Caption { text: "Keys: h/l or 1–6 switch tabs · j/k scroll · r check again · b btop · Esc close" }
}
