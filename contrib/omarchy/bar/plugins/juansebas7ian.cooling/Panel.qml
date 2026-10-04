import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

// CPU and cooling: live CPU temperature in the bar; the panel graphs the last
// two minutes of temperature, load and cooler speed, shows every thread's load
// and clock, the AIO pump and fans, and all temperatures. Warns (red icon and
// a notification) when the CPU reaches 90 °C or the pump stops.
Panel {
  id: root
  moduleName: "juansebas7ian.cooling"
  ipcTarget: "juansebas7ian.cooling"
  manageIpc: false

  readonly property string backend: String(Qt.resolvedUrl("cooling.py")).replace("file://", "")
  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property color urgent: bar ? bar.urgent : Color.urgent
  readonly property color dim: Qt.darker(foreground, 1.55)
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family
  readonly property int historySize: 120

  property var s: null
  property var tempHist: []
  property var loadHist: []
  property var coolerHist: []
  property real tempPeak: 0
  property var fanMax: ({})
  property bool pumpAlerted: false

  readonly property real tctl: s && s.cpu.tctl !== null ? s.cpu.tctl : 0
  readonly property var pump: s ? s.fans.filter(function(f) { return f.role === "pump" })[0] : null
  readonly property var cpuFans: s ? s.fans.filter(function(f) { return f.role === "cpu" }) : []
  readonly property bool pumpFailed: !!pump && pump.rpm < 500
  readonly property bool hot: tctl >= 90
  readonly property bool alarming: hot || pumpFailed

  function push(list, value) {
    var out = list.slice(Math.max(0, list.length - historySize + 1))
    out.push(value)
    return out
  }
  function deg(t) { return t === null || t === undefined ? "–" : Math.round(t) + "°C" }

  function ingest(snap) {
    s = snap
    tempHist = push(tempHist, snap.cpu.tctl || 0)
    loadHist = push(loadHist, snap.cpu.load || 0)
    var cooler = 0
    for (var i = 0; i < snap.fans.length; i++) if (snap.fans[i].role === "cpu") cooler = Math.max(cooler, snap.fans[i].rpm)
    coolerHist = push(coolerHist, cooler)
    tempPeak = Math.max(tempPeak, snap.cpu.tctl || 0)
    var m = Object.assign({}, fanMax)
    for (var j = 0; j < snap.fans.length; j++) m[snap.fans[j].id] = Math.max(m[snap.fans[j].id] || 0, snap.fans[j].rpm)
    fanMax = m
    if (pumpFailed && !pumpAlerted) {
      pumpAlerted = true
      Quickshell.execDetached(["notify-send", "-u", "critical", "-a", "Cooling", "AIO pump stopped",
        "The CPU pump reads " + pump.rpm + " RPM. Check the pump cable (AIO_PUMP) or the cooler before using the PC hard."])
    } else if (!pumpFailed) {
      pumpAlerted = false
    }
  }

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  onOpenedChanged: if (opened) Qt.callLater(function() { keyCatcher.forceActiveFocus() })

  Process {
    id: stream
    running: true
    command: ["python3", root.backend, "1"]
    stdout: SplitParser {
      onRead: function(line) { try { root.ingest(JSON.parse(line)) } catch (e) {} }
    }
    onExited: restart.start()
  }
  Timer { id: restart; interval: 3000; onTriggered: stream.running = true }

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
    text: "󰔏 " + (root.s ? Math.round(root.tctl) + "°" : "")
    active: root.alarming
    tooltipText: !root.s ? "CPU & cooling"
      : "CPU " + Math.round(root.tctl) + "°C · " + Math.round(root.s.cpu.load) + "%"
        + (root.pump ? " · pump " + root.pump.rpm + " RPM" : "")
        + (root.cpuFans.length > 0 ? " · cooler " + root.cpuFans.map(function(f) { return f.rpm }).join("/") + " RPM" : "")
        + (root.pumpFailed ? " · PUMP STOPPED" : "")
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
    contentWidth: panel.fittedContentWidth(Style.space(440))
    contentHeight: panel.fittedContentHeight(column.implicitHeight, Style.space(840))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      onCloseRequested: root.close()
      onTabRequested: function(direction) { root.switchPanel(direction) }

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
            title: root.s ? root.s.cpu.model.replace(/ \d+-Core Processor/, "") : "CPU"
            meta: root.s ? Math.round(root.tctl) + "°C · " + Math.round(root.s.cpu.load) + "% · "
              + (root.s.cpu.freqMax / 1000).toFixed(2) + " GHz max" : "Reading sensors…"
            foreground: root.foreground
            fontFamily: root.fontFamily
            iconComponent: Component {
              Text { text: "󰔏"; color: root.hot ? root.urgent : root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.display }
            }
          }

          Text {
            visible: root.pumpFailed
            width: parent.width
            text: "⚠ The AIO pump reads " + (root.pump ? root.pump.rpm : 0) + " RPM. Without the pump the CPU overheats in seconds under load."
            color: root.urgent
            font.family: root.fontFamily
            font.pixelSize: Style.font.bodySmall
            font.bold: true
            wrapMode: Text.WordWrap
          }

          // ---------------------------------------------------------- graphs
          Graph {
            label: "CPU temperature · now " + root.deg(root.tctl) + " · 2-min peak " + root.deg(root.tempPeak)
            values: root.tempHist
            minValue: 30
            maxValue: 95
            warnAt: 85
          }
          Graph {
            label: "CPU load · " + (root.s ? Math.round(root.s.cpu.load) : 0) + "% · load avg " + (root.s ? root.s.cpu.loadavg.join(" / ") : "")
            values: root.loadHist
            minValue: 0
            maxValue: 100
          }
          Graph {
            visible: root.cpuFans.length > 0
            label: "CPU cooler fans · " + root.cpuFans.map(function(f) { return f.rpm }).join(" / ") + " RPM"
            values: root.coolerHist
            minValue: 0
            maxValue: Math.max(2200, Math.max.apply(null, root.coolerHist.concat([1])))
          }

          // ---------------------------------------------------------- threads
          Section { title: "THREADS · LOAD AND CLOCK" }
          Row {
            id: threadRow
            width: parent.width
            spacing: Style.space(3)
            readonly property int count: root.s ? root.s.cpu.threads.length : 12
            Repeater {
              model: root.s ? root.s.cpu.threads : []
              Column {
                required property var modelData
                required property int index
                width: (threadRow.width - threadRow.spacing * (threadRow.count - 1)) / threadRow.count
                spacing: Style.space(2)
                Item {
                  width: parent.width
                  height: Style.space(46)
                  Rectangle { anchors.fill: parent; radius: Style.space(2); color: Qt.alpha(root.foreground, 0.1) }
                  Rectangle {
                    anchors.bottom: parent.bottom
                    width: parent.width
                    radius: Style.space(2)
                    height: parent.height * Math.max(0.02, modelData / 100)
                    color: modelData > 90 ? root.urgent : root.foreground
                    Behavior on height { NumberAnimation { duration: 300 } }
                  }
                }
                Text {
                  width: parent.width
                  horizontalAlignment: Text.AlignHCenter
                  text: root.s && root.s.cpu.freqs[index] ? (root.s.cpu.freqs[index] / 1000).toFixed(1) : ""
                  color: root.dim
                  font.family: root.fontFamily
                  font.pixelSize: Style.font.caption - 1
                }
              }
            }
          }
          Pair {
            visible: !!root.s
            small: true
            label: "Governor · energy preference"
            value: root.s ? root.s.cpu.governor + " · " + root.s.cpu.epp : ""
          }

          // ---------------------------------------------------------- cooling
          Section { title: "COOLING" }
          Repeater {
            model: root.s ? root.s.fans : []
            Column {
              id: fanRow
              required property var modelData
              width: column.width
              spacing: Style.space(2)
              readonly property real peak: Math.max(fanRow.modelData.rpm, root.fanMax[fanRow.modelData.id] || 1)
              Pair {
                label: (fanRow.modelData.role === "pump" ? "󰈐 " : fanRow.modelData.role === "cpu" ? "󰈐 " : "󰈐 ") + fanRow.modelData.name
                  + (fanRow.modelData.role === "pump" ? (root.pumpFailed ? "  · STOPPED" : "  · running") : "")
                value: fanRow.modelData.rpm + " RPM"
                hot: fanRow.modelData.role === "pump" && root.pumpFailed
                strong: fanRow.modelData.role !== "case"
              }
              Item {
                width: parent.width
                height: Style.space(4)
                Rectangle { anchors.fill: parent; radius: height / 2; color: Qt.alpha(root.foreground, 0.12) }
                Rectangle {
                  height: parent.height
                  radius: height / 2
                  color: fanRow.modelData.role === "pump" && root.pumpFailed ? root.urgent : root.foreground
                  width: parent.width * Math.min(1, fanRow.modelData.rpm / Math.max(1, fanRow.peak))
                  Behavior on width { NumberAnimation { duration: 400 } }
                }
              }
            }
          }
          Text {
            width: parent.width
            text: "Bars are relative to each fan's highest speed seen since the bar started. Rename fans in ~/.config/omarchy-sysmon/fans.json."
            color: root.dim
            font.family: root.fontFamily
            font.pixelSize: Style.font.caption
            wrapMode: Text.WordWrap
          }

          // ---------------------------------------------------------- temperatures
          Section { title: "TEMPERATURES" }
          Pair { visible: !!root.s; label: "CPU (Tctl)"; value: root.deg(root.tctl); hot: root.hot }
          Repeater {
            model: root.s ? root.s.cpu.ccd : []
            Pair { required property var modelData; required property int index; label: "CPU die (CCD" + (index + 1) + ")"; value: root.deg(modelData) }
          }
          Repeater {
            model: root.s ? root.s.board : []
            Pair { required property var modelData; label: modelData.name; value: root.deg(modelData.temp) }
          }
          Pair { visible: !!root.s && root.s.ram.length > 0; label: "DDR5 modules"; value: root.s ? root.s.ram.map(root.deg).join("  ") : "" }
          Pair {
            visible: !!root.s && !!root.s.gpu
            label: "GPU (RTX 3060)"
            value: root.s && root.s.gpu ? root.deg(root.s.gpu.temp) + " · fan " + (root.s.gpu.fan === null ? "–" : root.s.gpu.fan + "%") + " · " + root.s.gpu.util + "%" : ""
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

  component Graph: Column {
    id: graph
    property string label: ""
    property var values: []
    property real minValue: 0
    property real maxValue: 100
    property real warnAt: -1
    width: parent ? parent.width : 0
    spacing: Style.space(3)
    Text { text: graph.label; color: root.foreground; opacity: 0.75; font.family: root.fontFamily; font.pixelSize: Style.font.caption }
    Canvas {
      id: canvas
      width: parent.width
      height: Style.space(54)
      onPaint: {
        var ctx = getContext("2d")
        ctx.reset()
        ctx.fillStyle = Qt.alpha(root.foreground, 0.07)
        ctx.fillRect(0, 0, width, height)
        var span = Math.max(1e-6, graph.maxValue - graph.minValue)
        function y(v) { return height - (Math.max(graph.minValue, Math.min(graph.maxValue, v)) - graph.minValue) / span * height }
        if (graph.warnAt > 0) {
          ctx.strokeStyle = Qt.alpha(root.urgent, 0.5)
          ctx.setLineDash([3, 3])
          ctx.beginPath(); ctx.moveTo(0, y(graph.warnAt)); ctx.lineTo(width, y(graph.warnAt)); ctx.stroke()
          ctx.setLineDash([])
        }
        var n = graph.values.length
        if (n < 2) return
        var step = width / (root.historySize - 1)
        var x0 = width - (n - 1) * step
        ctx.beginPath()
        ctx.moveTo(x0, height)
        for (var i = 0; i < n; i++) ctx.lineTo(x0 + i * step, y(graph.values[i]))
        ctx.lineTo(width, height)
        ctx.closePath()
        ctx.fillStyle = Qt.alpha(root.foreground, 0.18)
        ctx.fill()
        ctx.beginPath()
        for (var j = 0; j < n; j++) {
          if (j === 0) ctx.moveTo(x0, y(graph.values[0]))
          else ctx.lineTo(x0 + j * step, y(graph.values[j]))
        }
        ctx.strokeStyle = graph.warnAt > 0 && graph.values[n - 1] >= graph.warnAt ? root.urgent : root.foreground
        ctx.lineWidth = 1.5
        ctx.stroke()
      }
    }
    onValuesChanged: if (root.opened) canvas.requestPaint()
    Connections { target: root; function onOpenedChanged() { if (root.opened) canvas.requestPaint() } }
  }

  component Pair: Item {
    property string label: ""
    property string value: ""
    property bool hot: false
    property bool strong: false
    property bool small: false
    width: parent ? parent.width : 0
    implicitHeight: Math.max(pl.implicitHeight, pv.implicitHeight)
    Text {
      id: pl
      anchors.left: parent.left
      anchors.right: pv.left
      anchors.rightMargin: Style.space(8)
      text: parent.label
      color: parent.hot ? root.urgent : root.foreground
      opacity: parent.strong || parent.hot ? 1.0 : 0.6
      font.family: root.fontFamily
      font.pixelSize: parent.small ? Style.font.caption : Style.font.bodySmall
      elide: Text.ElideRight
    }
    Text {
      id: pv
      anchors.right: parent.right
      text: parent.value
      color: parent.hot ? root.urgent : root.foreground
      font.family: root.fontFamily
      font.pixelSize: parent.small ? Style.font.caption : Style.font.bodySmall
      font.bold: parent.strong
    }
  }
}
