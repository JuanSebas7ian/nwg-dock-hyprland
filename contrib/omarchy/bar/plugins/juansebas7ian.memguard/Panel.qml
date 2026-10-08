import QtQuick
import QtQuick.Controls
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui
import "ui"

// Memory guard: the face of omarchy-memguard. Everything comes from
// `omarchy-memguard ui` (JSON) and every change goes through its validated
// set/add/remove/reset commands; the daemon reloads the settings live.
// Tabs: tabs/<Name>Tab.qml, each gets this object as `mg`.
Panel {
  id: root
  moduleName: "juansebas7ian.memguard"
  ipcTarget: "juansebas7ian.memguard"
  manageIpc: false

  readonly property string bin: Quickshell.env("HOME") + "/.local/bin/omarchy-memguard"
  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property color urgent: bar ? bar.urgent : Color.urgent
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family

  Binding { target: Theme; property: "foreground"; value: root.foreground }
  Binding { target: Theme; property: "urgent"; value: root.urgent }
  Binding { target: Theme; property: "fontFamily"; value: root.fontFamily }

  property string tab: "overview"
  readonly property var tabFiles: ({ overview: "OverviewTab", apps: "AppsTab", activity: "ActivityTab", settings: "SettingsTab" })
  readonly property var tabOrder: ["overview", "apps", "activity", "settings"]

  property var ui: null            // last `omarchy-memguard ui` answer
  property string message: ""
  property bool messageBad: false
  property string busy: ""         // what an action is changing right now
  property var smoke: []           // self-check lines

  readonly property var st: ui && ui.status ? ui.status : null
  readonly property bool running: !!ui && ui.service === "active"
  readonly property bool enabled: !st || st.enabled !== false
  readonly property int level: st ? (st.level || 0) : 0
  readonly property int vlevel: st ? (st.vramLevel || 0) : 0
  readonly property var ram: st ? st.ram : null
  readonly property var vram: st ? st.vram : null
  readonly property var cfg: ui && ui.config ? ui.config.effective : null
  readonly property bool acting: !!st && ((st.frozen || []).length > 0 || (st.throttled || []).length > 0)
  readonly property bool alarm: !running || level >= 2 || vlevel >= 2

  readonly property var tabs: [
    { id: "overview", label: "Overview", badge: level > 0 ? String(level) : "", hot: alarm },
    { id: "apps", label: "Apps", badge: "", hot: false },
    { id: "activity", label: "Activity", badge: "", hot: false },
    { id: "settings", label: "Settings", badge: enabled ? "" : "off", hot: false }
  ]

  function setTab(id) { if (tabFiles[id]) tab = id }
  function stepTab(d) {
    var i = tabOrder.indexOf(tab)
    setTab(tabOrder[(i + d + tabOrder.length) % tabOrder.length])
  }
  function refresh() {
    if (poll.running) return
    poll.command = opened ? [bin, "ui"] : [bin, "ui", "--brief"]
    poll.running = true
  }
  // op: set | add | remove | reset
  function change(op, key, value, what) {
    if (action.running) return
    busy = what || key || op
    var cmd = [bin, op]
    if (key !== undefined && key !== null) cmd.push(String(key))
    if (value !== undefined && value !== null) cmd.push(String(value))
    action.command = cmd
    action.running = true
  }
  function setValue(key, value) { change("set", key, value, key) }
  function protect(name) { change("add", "protect", name, name) }
  function makeOrdinary(name) { change("add", "ordinary", name, name) }
  function forget(list, name) { change("remove", list, name, name) }
  function selfCheck() { if (!check.running) { smoke = []; check.running = true } }
  function startService() { if (bar) bar.run("systemctl --user restart omarchy-memguard.service"); message = "Starting…"; messageBad = false }
  function openLog() { if (bar) bar.run("xdg-open " + bar.shellQuote(Quickshell.env("HOME") + "/.local/state/omarchy-memguard/events.log")); close() }
  function bytes(n) { return Theme.bytes(n || 0) }
  function kindLabel(k) {
    return k === "desktop" ? "Desktop" : k === "ai" ? "AI" : k === "dev" ? "Work" : k === "user" ? "Yours" : ""
  }

  onOpenedChanged: {
    if (opened) {
      refresh()
      Qt.callLater(function() { keyCatcher.forceActiveFocus() })
    } else {
      message = ""
      smoke = []
    }
  }
  onTabChanged: { message = ""; loadTab() }

  Timer {
    interval: root.opened ? 2000 : 10000
    running: true
    repeat: true
    triggeredOnStart: true
    onTriggered: root.refresh()
  }

  Process {
    id: poll
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        try {
          var d = JSON.parse(text)
          // the brief answer carries no apps/config: keep the last full ones
          if (root.ui && d.apps === undefined) d = Object.assign({}, root.ui, { status: d.status, service: d.service, age: d.age })
          root.ui = d
        } catch (e) {}
      }
    }
  }
  Process {
    id: action
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        var r = null
        try { r = JSON.parse(text) } catch (e) {}
        root.messageBad = !r || r.ok !== true
        root.message = !r ? "No answer from omarchy-memguard" : r.ok ? "Saved: the guard uses it from now on." : r.error
        root.busy = ""
        poll.running = false
        root.refresh()
      }
    }
  }
  Process {
    id: check
    command: [root.bin, "smoke"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        root.smoke = text.split("\n").filter(function(l) { return l.trim() !== "" }).map(function(l) {
          var i = l.indexOf(" ")
          return { status: l.slice(0, i), text: l.slice(i + 1) }
        })
      }
    }
  }

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
    // Text-bearing icon: grow with the painted text (BarIconButton is one icon wide).
    fixedWidth: vertical ? -1 : Math.max(slotSize, Math.ceil(glyphPaintedWidth) + Style.space(10))
    text: "󰘚" + (!root.enabled ? " off" : root.level > 0 ? " " + root.level : "")
    active: root.alarm
    tooltipText: !root.ui ? "Memory guard"
      : !root.running ? "Memory guard is not running"
      : "RAM " + Theme.gb(root.ram ? root.ram.used : 0) + "/" + Theme.gb(root.ram ? root.ram.total : 0) + " GB"
        + " · work waits " + (root.ram ? root.ram.psiSome : 0) + "%"
        + (root.vram ? " · VRAM " + Theme.gb(root.vram.used) + "/" + Theme.gb(root.vram.total) + " GB" : "")
        + (!root.enabled ? "\nWatching only (switched off)" : root.level > 0 ? "\nLevel " + root.level + ": " + (root.st.reasons || []).join("; ") : "")
        + (root.acting ? "\nActing: " + (root.st.frozen || []).concat(root.st.throttled || []).join(", ") : "")
        + (root.st && root.st.dictation ? "\nDictation: " + root.st.dictation : "")
    onPressed: function(code) {
      if (code === Qt.RightButton) root.setValue("enabled", root.enabled ? 0 : 1)
      else root.toggle()
    }
  }

  // ------------------------------------------------------------------ panel
  function loadTab() {
    flick.contentY = 0
    loader.setSource(Qt.resolvedUrl("tabs/" + tabFiles[tab] + ".qml"), { mg: root })
  }
  Component.onCompleted: loadTab()

  KeyboardPanel {
    id: panel
    anchorItem: button
    owner: root
    bar: root.bar
    open: root.opened
    focusTarget: keyCatcher
    contentWidth: panel.fittedContentWidth(Style.space(470))
    contentHeight: panel.fittedContentHeight(header.implicitHeight + Style.space(10) + (loader.item ? loader.item.implicitHeight : 0), Style.space(1000))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      onMoveRequested: function(dx, dy) {
        if (dx !== 0) root.stepTab(dx)
        if (dy !== 0) flick.contentY = Math.max(0, Math.min(flick.contentHeight - flick.height, flick.contentY + dy * Style.space(56)))
      }
      onCloseRequested: root.close()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onTextKey: function(t) {
        if (t === "r" || t === "R") { poll.running = false; root.refresh() }
        else if (t >= "1" && t <= "4") root.setTab(root.tabOrder[Number(t) - 1])
      }

      Column {
        id: header
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.top: parent.top
        spacing: Style.space(10)

        PanelHero {
          width: parent.width
          title: "Memory guard"
          meta: !root.ui ? "Reading…"
            : !root.running ? "Not running"
            : !root.enabled ? "Watching only · switched off"
            : root.level === 0 && root.vlevel === 0 ? "All calm · RAM " + Theme.gb(root.ram ? root.ram.used : 0) + " GB"
              + (root.vram ? " · VRAM " + Theme.gb(root.vram.used) + " GB" : "")
            : "Level " + root.level + (root.acting ? " · acting" : "") + (root.vlevel > 0 ? " · GPU memory low" : "")
          foreground: root.foreground
          fontFamily: root.fontFamily
          iconComponent: Component {
            Text { text: "󰘚"; color: root.alarm ? root.urgent : root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.display }
          }
          trailingControl: Component {
            PanelActionButton {
              iconText: "󰑐"
              tooltipText: "Refresh (r)"
              foreground: root.foreground
              fontFamily: root.fontFamily
              onClicked: { poll.running = false; root.refresh() }
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
          level: root.messageBad ? "crit" : "info"
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
          // Constant scrollbar gutter: a width that depends on flick.interactive is a layout loop (2026-10-08).
          width: flick.width - Style.space(12)
          onLoaded: item.width = Qt.binding(function() { return loader.width })
        }
      }
    }
  }
}
