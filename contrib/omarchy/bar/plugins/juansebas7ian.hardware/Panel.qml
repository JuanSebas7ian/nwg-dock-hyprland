import QtQuick
import QtQuick.Controls
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui
import "ui"

// Hardware: one bar icon and one panel with tabs for the whole machine.
// All data comes from backend/hardwared.py, a single long-running process
// that streams JSON lines; the panel tells it which tab is visible so it
// samples fast only what is on screen. Each tab lives in tabs/<Name>Tab.qml
// and gets this object as `hw`.
Panel {
  id: root
  moduleName: "juansebas7ian.hardware"
  ipcTarget: "juansebas7ian.hardware"
  manageIpc: false

  readonly property string backend: String(Qt.resolvedUrl("backend/hardwared.py")).replace("file://", "")
  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property color urgent: bar ? bar.urgent : Color.urgent
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family
  readonly property int historySize: 120

  Binding { target: Theme; property: "foreground"; value: root.foreground }
  Binding { target: Theme; property: "urgent"; value: root.urgent }
  Binding { target: Theme; property: "fontFamily"; value: root.fontFamily }

  // ------------------------------------------------------------------ state
  property string tab: "summary"
  readonly property var tabFiles: ({ summary: "SummaryTab", cpu: "CpuTab", gpu: "GpuTab",
                                     storage: "StorageTab", drivers: "DriversTab", devices: "DevicesTab" })
  readonly property var tabOrder: ["summary", "cpu", "gpu", "storage", "drivers", "devices"]

  property var live: null
  property var hist: ({ tctl: [], load: [], cooler: [], gpu: [] })
  property real tempPeak: 0
  property var fanMax: ({})
  property var gpuProcs: []
  property var drivers: null
  property var storage: ({ overview: null, breakdown: null, cleanup: null, steam: null, firmware: null })
  property var dirView: null
  property var dirStack: []
  property var devices: null
  property var alerts: []
  property string message: ""
  property var pendingBt: ({})

  readonly property var cpu: live ? live.cpu : null
  readonly property real tctl: cpu && cpu.tctl !== null ? cpu.tctl : 0
  readonly property var gpu: live && live.gpus && live.gpus.length > 0 ? live.gpus[0] : null
  readonly property var pump: live ? live.fans.filter(function(f) { return f.role === "pump" })[0] : null
  readonly property int critCount: alerts.filter(function(a) { return a.level === "crit" }).length
  readonly property int updateCount: drivers ? drivers.updates.drivers.length + (drivers.bios && drivers.bios.newer ? 1 : 0) : 0

  function alertsFor(t) { return alerts.filter(function(a) { return a.tab === t }) }
  function tabBadge(t) {
    var n = alertsFor(t).length
    if (t === "drivers" && updateCount > 0) return String(updateCount)
    return n > 0 ? "!" : ""
  }
  readonly property var tabs: [
    { id: "summary", label: "Summary", badge: alerts.length > 0 ? String(alerts.length) : "", hot: critCount > 0 },
    { id: "cpu", label: "CPU", badge: tabBadge("cpu"), hot: alertsFor("cpu").some(function(a) { return a.level === "crit" }) },
    { id: "gpu", label: "GPU", badge: tabBadge("gpu"), hot: false },
    { id: "storage", label: "Disks", badge: tabBadge("storage"), hot: alertsFor("storage").some(function(a) { return a.level === "crit" }) },
    { id: "drivers", label: "Drivers", badge: tabBadge("drivers"), hot: alertsFor("drivers").some(function(a) { return a.level === "crit" }) },
    { id: "devices", label: "Devices", badge: tabBadge("devices"), hot: false }
  ]

  // ------------------------------------------------------------------ backend
  function send(obj) { if (proc.running) proc.write(JSON.stringify(obj) + "\n") }
  function sendView() { send({ cmd: "view", open: root.opened, tab: root.tab }) }
  function setTab(id) {
    if (!tabFiles[id]) return
    tab = id
    dirView = null
    dirStack = []
  }
  function stepTab(d) {
    var i = tabOrder.indexOf(tab)
    setTab(tabOrder[(i + d + tabOrder.length) % tabOrder.length])
  }
  function refreshTab() {
    if (tab === "drivers") send({ cmd: "refresh", what: "drivers", force: true })
    else if (tab === "storage") send({ cmd: "refresh", what: "storage" })
    else if (tab === "devices") send({ cmd: "refresh", what: "peripherals" })
    else send({ cmd: "refresh", what: "drivers" })
  }
  function openDir(path) {
    dirStack = dirView ? dirStack.concat([dirView.path]) : []
    send({ cmd: "dir", path: path })
  }
  function back() {
    if (dirStack.length === 0) { dirView = null; return }
    var prev = dirStack[dirStack.length - 1]
    dirStack = dirStack.slice(0, -1)
    send({ cmd: "dir", path: prev })
  }
  function bt(action, mac, on) {
    var p = Object.assign({}, pendingBt); p[mac || "adapter"] = action; pendingBt = p
    send({ cmd: "bt", action: action, mac: mac, on: on })
  }
  function inTerminal(cmd) {
    root.bar.run("omarchy-launch-floating-terminal-with-presentation " + root.bar.shellQuote(cmd))
    root.close()
  }
  function runAndClose(cmd) { root.bar.run(cmd); root.close() }
  function push(list, value) {
    var out = list.slice(Math.max(0, list.length - historySize + 1))
    out.push(value)
    return out
  }

  function ingest(msg) {
    var d = msg.data
    switch (msg.type) {
    case "live":
      live = d
      if (d.gpus && d.gpus.length > 0 && d.gpus[0].processes) gpuProcs = d.gpus[0].processes
      var cooler = 0
      for (var i = 0; i < d.fans.length; i++) if (d.fans[i].role === "cpu") cooler = Math.max(cooler, d.fans[i].rpm)
      hist = { tctl: push(hist.tctl, d.cpu.tctl || 0), load: push(hist.load, d.cpu.load || 0),
               cooler: push(hist.cooler, cooler), gpu: push(hist.gpu, d.gpus && d.gpus.length > 0 ? d.gpus[0].util : 0) }
      tempPeak = Math.max(tempPeak, d.cpu.tctl || 0)
      var m = Object.assign({}, fanMax)
      for (var j = 0; j < d.fans.length; j++) m[d.fans[j].id] = Math.max(m[d.fans[j].id] || 0, d.fans[j].rpm)
      fanMax = m
      break
    case "history":
      hist = { tctl: d.map(function(s) { return s.tctl || 0 }), load: d.map(function(s) { return s.load || 0 }),
               cooler: d.map(function(s) { return s.cooler || 0 }), gpu: d.map(function(s) { return s.gpu || 0 }) }
      tempPeak = Math.max.apply(null, hist.tctl.concat([0]))
      break
    case "drivers": drivers = d; break
    case "storage.dir": dirView = d; break
    case "peripherals": devices = d; break
    case "alerts": alerts = d; break
    case "result":
      if (d.mac !== undefined) { var p = Object.assign({}, pendingBt); delete p[d.mac || "adapter"]; pendingBt = p }
      message = d.ok ? (d.action === "selftest" ? "Short self-test started (about 2 min)." : "")
                     : (d.action || "Action") + " failed: " + (d.error || "unknown error")
      break
    default:
      if (msg.type.indexOf("storage.") === 0) {
        var s = Object.assign({}, storage)
        s[msg.type.slice(8)] = d
        storage = s
      }
    }
  }

  onOpenedChanged: {
    sendView()
    if (opened) Qt.callLater(function() { keyCatcher.forceActiveFocus() })
    else message = ""
  }
  onTabChanged: { sendView(); message = ""; loadTab() }

  Process {
    id: proc
    running: true
    stdinEnabled: true
    command: ["/usr/bin/python3", "-B", root.backend]
    stdout: SplitParser {
      onRead: function(line) { try { root.ingest(JSON.parse(line)) } catch (e) {} }
    }
    onRunningChanged: if (running) Qt.callLater(root.sendView)
    // Never leave the bar without data: restart if the backend dies.
    onExited: restartTimer.start()
  }
  Timer { id: restartTimer; interval: 3000; onTriggered: proc.running = true }

  IpcHandler {
    target: root.ipcTarget
    function open(): void { root.open() }
    function close(): void { root.close() }
    function toggle(): void { root.toggle() }
    function tab(name: string): void { root.setTab(name); root.open() }
  }

  // ------------------------------------------------------------------ bar icon
  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    // BarIconButton is one icon wide; this widget also shows text (counts, °, %, country),
    // which spilled over its neighbours. Grow with the painted text instead.
    fixedWidth: vertical ? -1 : Math.max(slotSize, Math.ceil(glyphPaintedWidth) + Style.space(10))
    text: "󰔏 " + (root.cpu ? Math.round(root.tctl) + "°" : "") + (root.updateCount > 0 ? "  " + root.updateCount : "")
    active: root.alerts.length > 0
    tooltipText: !root.live ? "Hardware"
      : "CPU " + Math.round(root.tctl) + "°C " + Math.round(root.cpu.load) + "%"
        + (root.gpu ? " · GPU " + root.gpu.temp + "°C " + root.gpu.util + "%" : "")
        + " · RAM " + Theme.gb(root.live.mem.used) + "/" + Theme.gb(root.live.mem.total) + " GB"
        + (root.pump ? " · pump " + root.pump.rpm + " RPM" : "")
        + (root.alerts.length > 0 ? "\n" + root.alerts.map(function(a) { return (a.level === "crit" ? "✖ " : "⚠ ") + a.title }).join("\n") : "")
        + (root.updateCount > 0 ? "\n" + root.updateCount + " driver/BIOS update(s)" : "")
    onPressed: function(code) {
      if (code === Qt.RightButton) { if (root.bar) root.bar.run("omarchy-launch-or-focus-tui btop") }
      else root.toggle()
    }
  }

  // ------------------------------------------------------------------ panel
  function loadTab() {
    flick.contentY = 0
    loader.setSource(Qt.resolvedUrl("tabs/" + tabFiles[tab] + ".qml"), { hw: root })
  }
  Component.onCompleted: loadTab()

  KeyboardPanel {
    id: panel
    anchorItem: button
    owner: root
    bar: root.bar
    open: root.opened
    focusTarget: keyCatcher
    contentWidth: panel.fittedContentWidth(Style.space(460))
    contentHeight: panel.fittedContentHeight(header.implicitHeight + Style.space(10) + (loader.item ? loader.item.implicitHeight : 0), Style.space(1100))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      onMoveRequested: function(dx, dy) {
        if (dx !== 0) root.stepTab(dx)
        if (dy !== 0) flick.contentY = Math.max(0, Math.min(flick.contentHeight - flick.height, flick.contentY + dy * Style.space(56)))
      }
      onCloseRequested: root.dirView ? root.back() : root.close()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onTextKey: function(t) {
        if (t === "r" || t === "R") root.refreshTab()
        else if (t === "b") { root.runAndClose("omarchy-launch-or-focus-tui btop") }
        else if (t >= "1" && t <= "6") root.setTab(root.tabOrder[Number(t) - 1])
      }

      Column {
        id: header
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.top: parent.top
        spacing: Style.space(10)

        PanelHero {
          width: parent.width
          title: root.drivers ? root.drivers.board.name.replace(" GAMING WIFI", "") : "Hardware"
          meta: !root.live ? "Reading sensors…"
            : root.alerts.length === 0 ? "All good · CPU " + Math.round(root.tctl) + "°C" + (root.gpu ? " · GPU " + root.gpu.temp + "°C" : "")
            : root.alerts.length + (root.alerts.length === 1 ? " alert" : " alerts") + (root.critCount > 0 ? " · " + root.critCount + " critical" : "")
          foreground: root.foreground
          fontFamily: root.fontFamily
          iconComponent: Component {
            Text { text: "󰍛"; color: root.critCount > 0 ? root.urgent : root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.display }
          }
          trailingControl: Component {
            PanelActionButton {
              iconText: "󰑐"
              tooltipText: "Check again (r) · b = btop"
              foreground: root.foreground
              fontFamily: root.fontFamily
              onClicked: root.refreshTab()
            }
          }
        }

        TabBar {
          tabs: root.tabs
          current: root.tab
          onPicked: function(id) { root.setTab(id) }
        }

        Notice {
          visible: root.message !== ""
          text: root.message
          level: "warn"
        }
      }

      Flickable {
        id: flick
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.top: header.bottom
        anchors.topMargin: Style.space(10)
        anchors.bottom: parent.bottom
        contentWidth: width
        contentHeight: loader.item ? loader.item.implicitHeight : 0
        clip: true
        boundsBehavior: Flickable.StopAtBounds
        interactive: contentHeight > height
        ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

        Loader {
          id: loader
          // Leave room for the scrollbar so it never covers the value column.
          width: flick.width - (flick.interactive ? Style.space(12) : 0)
          onLoaded: item.width = Qt.binding(function() { return loader.width })
        }
      }
    }
  }
}
