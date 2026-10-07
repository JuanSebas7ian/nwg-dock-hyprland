import QtQuick
import qs.Commons
import "../ui"

// CPU & cooling: two-minute graphs, every thread's load and clock, memory,
// the AIO pump and fans, all temperatures, disk and network throughput.
Column {
  id: tab
  property var hw: null
  spacing: Style.space(10)

  readonly property var s: hw ? hw.live : null
  readonly property var cpu: s ? s.cpu : null
  readonly property var cpuFans: s ? s.fans.filter(function(f) { return f.role === "cpu" }) : []
  readonly property bool pumpFailed: !!hw && hw.alerts.some(function(a) { return a.id === "pump" })

  Repeater {
    model: tab.hw ? tab.hw.alertsFor("cpu") : []
    Notice { required property var modelData; level: modelData.level; text: modelData.title + " · " + modelData.detail }
  }

  // ------------------------------------------------------------ graphs
  Graph {
    label: "Temperature · now " + Theme.deg(tab.cpu ? tab.cpu.tctl : null) + " · 2-min peak " + Theme.deg(tab.hw ? tab.hw.tempPeak : null)
    values: tab.hw ? tab.hw.hist.tctl : []
    minValue: 30
    maxValue: 95
    warnAt: 85
  }
  Graph {
    label: "Load · " + (tab.cpu ? Math.round(tab.cpu.load) + "% · load avg " + tab.cpu.loadavg.join(" / ") : "")
    values: tab.hw ? tab.hw.hist.load : []
  }
  Graph {
    visible: tab.cpuFans.length > 0
    label: "CPU cooler · " + tab.cpuFans.map(function(f) { return f.rpm }).join(" / ") + " RPM"
    values: tab.hw ? tab.hw.hist.cooler : []
    maxValue: Math.max(2200, Math.max.apply(null, (tab.hw ? tab.hw.hist.cooler : []).concat([1])))
  }

  // ------------------------------------------------------------ threads
  Section { title: tab.cpu ? tab.cpu.model.replace(/ \d+-Core Processor/, "").toUpperCase() + " · THREADS" : "THREADS" }
  Row {
    id: threadRow
    width: parent.width
    spacing: Style.space(3)
    readonly property int count: tab.cpu ? tab.cpu.threads.length : 12
    Repeater {
      model: tab.cpu ? tab.cpu.threads : []
      Column {
        required property var modelData
        required property int index
        width: (threadRow.width - threadRow.spacing * (threadRow.count - 1)) / threadRow.count
        spacing: Style.space(2)
        Item {
          width: parent.width
          height: Style.space(42)
          Rectangle { anchors.fill: parent; radius: Style.space(2); color: Qt.alpha(Theme.foreground, 0.1) }
          Rectangle {
            anchors.bottom: parent.bottom
            width: parent.width
            radius: Style.space(2)
            height: parent.height * Math.max(0.02, modelData / 100)
            color: modelData > 90 ? Theme.urgent : Theme.foreground
            Behavior on height { NumberAnimation { duration: 300 } }
          }
        }
        Text {
          width: parent.width
          horizontalAlignment: Text.AlignHCenter
          text: tab.cpu && tab.cpu.freqs[index] ? (tab.cpu.freqs[index] / 1000).toFixed(1) : ""
          color: Theme.dim
          font.family: Theme.fontFamily
          font.pixelSize: Style.font.caption - 1
        }
      }
    }
  }
  Pair { visible: !!tab.cpu; small: true; label: "Bars = load per thread · numbers = GHz"; value: tab.cpu ? tab.cpu.governor + " · " + tab.cpu.epp : "" }

  // ------------------------------------------------------------ memory
  Section { title: "MEMORY" }
  Gauge {
    label: "RAM"
    value: tab.s ? Theme.gb(tab.s.mem.used) + " / " + Theme.gb(tab.s.mem.total) + " GB" : ""
    fraction: tab.s ? tab.s.mem.used / tab.s.mem.total : 0
    hot: !!tab.hw && tab.hw.alerts.some(function(a) { return a.id === "ram" })
  }
  Pair { label: "Swap · file cache"; value: tab.s ? Theme.gb(tab.s.mem.swapUsed) + " GB · " + Theme.gb(tab.s.mem.cached) + " GB" : "" }

  // ------------------------------------------------------------ cooling
  Section { title: "COOLING" }
  Repeater {
    model: tab.s ? tab.s.fans : []
    Column {
      id: fanRow
      required property var modelData
      width: tab.width
      spacing: Style.space(2)
      readonly property bool failed: modelData.role === "pump" && tab.pumpFailed
      Pair {
        label: "󰈐 " + fanRow.modelData.name + (fanRow.modelData.role === "pump" ? (fanRow.failed ? " · STOPPED" : " · running") : "")
        value: fanRow.modelData.rpm + " RPM"
        hot: fanRow.failed
        strong: fanRow.modelData.role !== "case"
      }
      Meter {
        fraction: fanRow.modelData.rpm / Math.max(1, fanRow.modelData.rpm, tab.hw.fanMax[fanRow.modelData.id] || 1)
        hot: fanRow.failed
        thickness: Style.space(4)
      }
    }
  }
  Caption { text: "Bars = each fan against its fastest speed seen. Names and roles: ~/.config/omarchy-sysmon/fans.json" }

  // ------------------------------------------------------------ temperatures
  Section { title: "TEMPERATURES" }
  Pair { visible: !!tab.cpu; label: "CPU (Tctl)"; value: Theme.deg(tab.cpu ? tab.cpu.tctl : null); hot: !!tab.hw && tab.hw.alertsFor("cpu").some(function(a) { return a.id.indexOf("cpu-") === 0 }) }
  Repeater {
    model: tab.cpu ? tab.cpu.ccd : []
    Pair { required property var modelData; required property int index; label: "CPU die (CCD" + (index + 1) + ")"; value: Theme.deg(modelData) }
  }
  Repeater {
    model: tab.s ? tab.s.board : []
    Pair { required property var modelData; label: modelData.name; value: Theme.deg(modelData.temp) }
  }
  Pair { visible: !!tab.s && tab.s.ram.length > 0; label: "DDR5 modules"; value: tab.s ? tab.s.ram.map(Theme.deg).join("  ") : "" }
  Repeater {
    model: tab.s ? tab.s.nvme : []
    Pair { required property var modelData; label: "NVMe · " + modelData.name; value: Theme.deg(modelData.temp) }
  }
  Pair { visible: !!tab.hw && !!tab.hw.gpu; label: "GPU · " + (tab.hw && tab.hw.gpu ? tab.hw.gpu.name : ""); value: tab.hw && tab.hw.gpu ? Theme.deg(tab.hw.gpu.temp) : "" }
  Pair { visible: !!tab.s && !!tab.s.igpu && tab.s.igpu.temp !== undefined && tab.s.igpu.temp !== null; label: "Radeon (iGPU)"; value: tab.s && tab.s.igpu ? Theme.deg(tab.s.igpu.temp) : "" }

  // ------------------------------------------------------------ I/O
  Section { title: "DISK · NETWORK" }
  Pair { label: "Disk read · write"; value: tab.s ? Theme.rate(tab.s.disk.read) + " · " + Theme.rate(tab.s.disk.write) : "" }
  Pair { label: "Network ↓ · ↑"; value: tab.s ? Theme.rate(tab.s.net.rx) + " · " + Theme.rate(tab.s.net.tx) : "" }
}
