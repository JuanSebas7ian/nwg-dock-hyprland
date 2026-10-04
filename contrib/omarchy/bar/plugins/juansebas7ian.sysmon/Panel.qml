import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

// System monitor: CPU, RAM, temperatures, NVIDIA (NVML) and Radeon GPUs,
// disk and network throughput. Data streams from sysmon.py every 2 s.
Panel {
  id: root
  moduleName: "juansebas7ian.sysmon"
  ipcTarget: "juansebas7ian.sysmon"
  manageIpc: false

  readonly property string backend: String(Qt.resolvedUrl("sysmon.py")).replace("file://", "")
  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property color urgent: bar ? bar.urgent : Color.urgent
  readonly property color dim: Qt.darker(foreground, 1.55)
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family

  property var s: null
  readonly property var gpu: s && s.gpus && s.gpus.length > 0 ? s.gpus[0] : null
  property var gpuProcs: []

  // Thresholds that turn a value (and the bar icon) red.
  readonly property bool cpuHot: !!s && s.temps.cpu !== null && s.temps.cpu >= 85
  readonly property bool gpuHot: !!gpu && gpu.temp >= 83
  readonly property bool ramFull: !!s && s.mem.used / s.mem.total >= 0.9
  readonly property bool alarming: cpuHot || gpuHot || ramFull

  function gb(n) { return (Number(n || 0) / 1073741824).toFixed(1) }
  function rate(bps) {
    var n = Number(bps || 0)
    if (n >= 1e9) return (n / 1e9).toFixed(1) + " GB/s"
    if (n >= 1e6) return (n / 1e6).toFixed(1) + " MB/s"
    if (n >= 1e3) return (n / 1e3).toFixed(0) + " KB/s"
    return n.toFixed(0) + " B/s"
  }
  function deg(t) { return t === null || t === undefined ? "–" : Math.round(t) + "°C" }

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  onOpenedChanged: if (opened) Qt.callLater(function() { keyCatcher.forceActiveFocus() })

  Process {
    id: stream
    running: true
    command: ["python3", root.backend, "2"]
    stdout: SplitParser {
      onRead: function(line) {
        try {
          var snap = JSON.parse(line)
          root.s = snap
          // Process lists arrive every other sample; keep the last one.
          if (snap.gpus && snap.gpus.length > 0 && snap.gpus[0].processes) root.gpuProcs = snap.gpus[0].processes
        } catch (e) {}
      }
    }
    // Never leave the bar without data: restart if the backend dies.
    onExited: restartTimer.start()
  }
  Timer { id: restartTimer; interval: 3000; onTriggered: stream.running = true }

  IpcHandler {
    target: root.ipcTarget
    function open(): void { root.open() }
    function close(): void { root.close() }
    function toggle(): void { root.toggle() }
  }

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: "󰍛"
    active: root.alarming
    tooltipText: !root.s ? "System monitor"
      : "CPU " + Math.round(root.s.cpu.usage) + "% " + root.deg(root.s.temps.cpu)
        + " · RAM " + root.gb(root.s.mem.used) + "/" + root.gb(root.s.mem.total) + " GB"
        + (root.gpu ? " · GPU " + root.gpu.util + "% " + root.deg(root.gpu.temp) : "")
    onPressed: function(code) {
      if (code === Qt.RightButton) { if (root.bar) root.bar.run("omarchy-launch-or-focus-tui btop") }
      else root.toggle()
    }
  }

  KeyboardPanel {
    id: panel
    anchorItem: button
    owner: root
    bar: root.bar
    open: root.opened
    focusTarget: keyCatcher
    contentWidth: panel.fittedContentWidth(Style.space(420))
    contentHeight: panel.fittedContentHeight(column.implicitHeight, Style.space(820))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      onCloseRequested: root.close()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onTextKey: function(t) { if (t === "b") { root.bar.run("omarchy-launch-or-focus-tui btop"); root.close() } }

      Flickable {
        id: flick
        anchors.fill: parent
        contentWidth: width
        contentHeight: column.implicitHeight
        clip: true
        boundsBehavior: Flickable.StopAtBounds
        interactive: contentHeight > height
        ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

        Column {
          id: column
          width: flick.width
          spacing: Style.space(10)

          PanelHero {
            width: parent.width
            title: "System"
            meta: root.s ? root.s.cpu.model.replace(/ \d+-Core Processor/, "") + " · " + (root.gpu ? root.gpu.name : "") : "Loading…"
            foreground: root.foreground
            fontFamily: root.fontFamily
            iconComponent: Component {
              Text { text: "󰍛"; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.display }
            }
            trailingControl: Component {
              PanelActionButton {
                iconText: ""
                tooltipText: "Open btop (b)"
                foreground: root.foreground
                fontFamily: root.fontFamily
                onClicked: { root.bar.run("omarchy-launch-or-focus-tui btop"); root.close() }
              }
            }
          }

          // ---------------------------------------------------------- CPU
          Section { title: "CPU" }
          Gauge {
            label: "Usage"
            value: root.s ? Math.round(root.s.cpu.usage) + "%  ·  " + (root.s.cpu.freqAvg / 1000).toFixed(2) + " GHz" : ""
            fraction: root.s ? root.s.cpu.usage / 100 : 0
          }
          Gauge {
            label: "Temperature"
            value: root.s ? root.deg(root.s.temps.cpu) : ""
            fraction: root.s && root.s.temps.cpu !== null ? root.s.temps.cpu / 95 : 0
            hot: root.cpuHot
          }
          Pair {
            label: "Load (1 / 5 / 15 min)"
            value: root.s ? root.s.cpu.load.map(function(l) { return l.toFixed(2) }).join(" / ") + "  ·  " + root.s.cpu.cores + " threads" : ""
          }

          // ---------------------------------------------------------- Memory
          Section { title: "MEMORY" }
          Gauge {
            label: "RAM"
            value: root.s ? root.gb(root.s.mem.used) + " / " + root.gb(root.s.mem.total) + " GB" : ""
            fraction: root.s ? root.s.mem.used / root.s.mem.total : 0
            hot: root.ramFull
          }
          Pair {
            label: "Swap · cache"
            value: root.s ? root.gb(root.s.mem.swapUsed) + " GB · " + root.gb(root.s.mem.cached) + " GB" : ""
          }
          Pair {
            visible: !!root.s && root.s.temps.ram.length > 0
            label: "DDR5 modules"
            value: root.s ? root.s.temps.ram.map(root.deg).join("  ") : ""
          }

          // ---------------------------------------------------------- NVIDIA
          Section { visible: !!root.gpu; title: root.gpu ? "GPU · " + root.gpu.name.toUpperCase() : "GPU" }
          Gauge {
            visible: !!root.gpu
            label: "Core (SM)"
            value: root.gpu ? root.gpu.util + "%  ·  " + root.gpu.clockCore + " MHz  ·  P" + root.gpu.pstate : ""
            fraction: root.gpu ? root.gpu.util / 100 : 0
          }
          Gauge {
            visible: !!root.gpu
            label: "Memory bandwidth"
            value: root.gpu ? root.gpu.memUtil + "%  ·  " + root.gpu.clockMem + " MHz" : ""
            fraction: root.gpu ? root.gpu.memUtil / 100 : 0
          }
          Gauge {
            visible: !!root.gpu
            label: "VRAM"
            value: root.gpu ? root.gb(root.gpu.vramUsed) + " / " + root.gb(root.gpu.vramTotal) + " GB" : ""
            fraction: root.gpu ? root.gpu.vramUsed / root.gpu.vramTotal : 0
          }
          Gauge {
            visible: !!root.gpu
            label: "Power"
            value: root.gpu && root.gpu.power !== null ? Math.round(root.gpu.power) + " / " + Math.round(root.gpu.powerLimit) + " W" : ""
            fraction: root.gpu && root.gpu.power !== null ? root.gpu.power / root.gpu.powerLimit : 0
          }
          Gauge {
            visible: !!root.gpu
            label: "Temperature · fan"
            value: root.gpu ? root.deg(root.gpu.temp) + "  ·  fan " + (root.gpu.fan === null ? "–" : root.gpu.fan + "%") : ""
            fraction: root.gpu ? root.gpu.temp / 90 : 0
            hot: root.gpuHot
          }
          Pair {
            visible: !!root.gpu
            label: root.gpu ? "PCIe Gen" + root.gpu.pcieGen + " x" + root.gpu.pcieWidth + " (max Gen" + root.gpu.pcieGenMax + ")" : ""
            value: root.gpu ? "↓ " + root.rate(root.gpu.pcieRx * 1024) + "  ↑ " + root.rate(root.gpu.pcieTx * 1024) : ""
          }
          Pair {
            visible: !!root.gpu
            label: "Video encoder · decoder"
            value: root.gpu ? root.gpu.enc + "% · " + root.gpu.dec + "%" : ""
          }

          // Who uses the GPU
          Column {
            visible: root.gpuProcs.length > 0
            width: parent.width
            spacing: Style.space(3)
            Text {
              text: "PROCESSES ON THE GPU"
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
            }
            Repeater {
              model: root.gpuProcs
              Row {
                required property var modelData
                width: column.width
                Text {
                  width: parent.width * 0.5
                  text: modelData.name
                  color: root.foreground
                  font.family: root.fontFamily
                  font.pixelSize: Style.font.bodySmall
                  elide: Text.ElideRight
                }
                Text {
                  width: parent.width * 0.5
                  horizontalAlignment: Text.AlignRight
                  text: (modelData.sm !== undefined && modelData.sm !== null ? modelData.sm + "% SM · " : "")
                    + (modelData.mem / 1048576).toFixed(0) + " MB" + (modelData.type.indexOf("C") >= 0 ? " · compute" : "")
                  color: root.dim
                  font.family: root.fontFamily
                  font.pixelSize: Style.font.bodySmall
                }
              }
            }
          }

          // ---------------------------------------------------------- Radeon
          Section { visible: !!root.s && !!root.s.igpu; title: "INTEGRATED GPU · RADEON" }
          Gauge {
            visible: !!root.s && !!root.s.igpu
            label: "Usage · temperature"
            value: root.s && root.s.igpu ? root.s.igpu.util + "%  ·  " + root.deg(root.s.temps.igpu) : ""
            fraction: root.s && root.s.igpu ? root.s.igpu.util / 100 : 0
          }

          // ---------------------------------------------------------- Board
          Section { visible: !!root.s && root.s.temps.fans.length > 0; title: "MOTHERBOARD" }
          Repeater {
            model: root.s ? root.s.temps.board : []
            Pair { required property var modelData; label: modelData.name; value: root.deg(modelData.temp) }
          }
          Repeater {
            model: root.s ? root.s.temps.fans : []
            Pair { required property var modelData; label: "󰈐 " + modelData.name; value: modelData.rpm + " RPM" }
          }

          // ---------------------------------------------------------- Storage / network
          Section { title: "STORAGE · NETWORK" }
          Repeater {
            model: root.s ? root.s.temps.nvme : []
            Pair {
              required property var modelData
              label: modelData.name
              value: root.deg(modelData.temp)
            }
          }
          Pair {
            label: "Disk read · write"
            value: root.s ? root.rate(root.s.disk.read) + " · " + root.rate(root.s.disk.write) : ""
          }
          Pair {
            label: "Network ↓ · ↑"
            value: root.s ? root.rate(root.s.net.rx) + " · " + root.rate(root.s.net.tx) : ""
          }
        }
      }
    }
  }

  // ---------------------------------------------------------------- parts

  component Section: Column {
    property string title: ""
    width: parent.width
    spacing: Style.space(6)
    PanelSeparator { foreground: root.foreground }
    PanelSectionHeader { text: parent.title; foreground: root.foreground; fontFamily: root.fontFamily }
  }

  component Gauge: Column {
    property string label: ""
    property string value: ""
    property real fraction: 0
    property bool hot: false
    width: parent.width
    spacing: Style.space(3)
    Pair { label: parent.label; value: parent.value; hot: parent.hot }
    Item {
      width: parent.width
      height: Style.space(5)
      Rectangle { anchors.fill: parent; radius: height / 2; color: Qt.alpha(root.foreground, 0.15) }
      Rectangle {
        width: parent.width * Math.max(0, Math.min(1, parent.parent.fraction))
        height: parent.height
        radius: height / 2
        color: parent.parent.hot ? root.urgent : root.foreground
        Behavior on width { NumberAnimation { duration: 400; easing.type: Easing.OutCubic } }
      }
    }
  }

  component Pair: Item {
    property string label: ""
    property string value: ""
    property bool hot: false
    width: parent ? parent.width : 0
    implicitHeight: Math.max(l.implicitHeight, v.implicitHeight)
    Text {
      id: l
      anchors.left: parent.left
      text: parent.label
      color: root.foreground
      opacity: 0.6
      font.family: root.fontFamily
      font.pixelSize: Style.font.bodySmall
    }
    Text {
      id: v
      anchors.right: parent.right
      text: parent.value
      color: parent.hot ? root.urgent : root.foreground
      font.family: root.fontFamily
      font.pixelSize: Style.font.bodySmall
    }
  }
}
