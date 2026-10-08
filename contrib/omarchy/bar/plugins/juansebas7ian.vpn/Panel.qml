import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui
import "ui"
import "parts"

// VPN: Surfshark (any of its locations, from one WireGuard key) and Proton VPN
// Free (the servers imported from its .conf files), as NetworkManager
// profiles. ~/.local/bin/omarchy-vpn (bar/bin/) does the work and prints JSON.
// Screens: home (where you are, connect), locations (picker with flags by
// region), details (everything about the tunnel), accounts (keys and servers).
// Each one is screens/<Name>Screen.qml and gets this object as `vpn`.
Panel {
  id: root
  moduleName: "juansebas7ian.vpn"
  ipcTarget: "juansebas7ian.vpn"
  manageIpc: false

  readonly property string ctl: Quickshell.env("HOME") + "/.local/bin/omarchy-vpn"
  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property color urgent: bar ? bar.urgent : Color.urgent
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family

  Binding { target: Theme; property: "foreground"; value: root.foreground }
  Binding { target: Theme; property: "urgent"; value: root.urgent }
  Binding { target: Theme; property: "fontFamily"; value: root.fontFamily }

  readonly property string glyphOn: String.fromCodePoint(0xF0565)   // shield-check
  readonly property string glyphOff: String.fromCodePoint(0xF0499)  // shield-outline

  // ------------------------------------------------------------------ state
  property string screen: "home"
  readonly property var screens: ({
    home: { file: "HomeScreen", title: "VPN" },
    locations: { file: "LocationsScreen", title: "Choose a location" },
    details: { file: "DetailsScreen", title: "Connection details" },
    accounts: { file: "AccountsScreen", title: "Accounts & servers" }
  })
  property var st: null
  property var locs: null
  property string busy: ""          // location id being connected, "down", "import", "toggle"
  property string message: ""
  property bool messageIsError: false
  property string provider: "surfshark"   // provider shown in the picker
  property var selected: null             // location to connect when nothing is active
  property var leakResult: null

  readonly property bool connected: !!st && st.connected
  readonly property var current: connected ? st.current : null
  readonly property var currentLoc: current ? current.location : null
  readonly property var ip4: st && st.ip ? st.ip.ipv4 : null
  readonly property var ip6: st && st.ip ? st.ip.ipv6 : null
  readonly property bool leak: !!st && st.ipv6Leak
  readonly property var inbox: st ? (st.inbox || []) : []
  readonly property bool surfsharkReady: !!st && st.setup.surfshark
  readonly property var ssKey: st ? st.setup.surfsharkKey : null
  readonly property var protonServers: st ? st.setup.proton : []
  // What the big button connects to: the pick, else the last one used.
  readonly property var target: selected ? selected : (st && st.last && st.last.location ? st.last.location : null)

  // ------------------------------------------------------------------ actions
  function refresh(force) {
    if (statusProc.running) return
    statusProc.command = [ctl, "status"].concat(force ? ["--refresh"] : [])
    statusProc.running = true
  }
  function loadLocations(force) {
    if (locProc.running) return
    locProc.command = [ctl, "locations"].concat(force ? ["--refresh"] : [])
    locProc.running = true
  }
  function run(tag, args) {
    if (actionProc.running) return
    busy = tag
    message = ""
    actionProc.command = [ctl].concat(args)
    actionProc.running = true
  }
  function connectTo(loc) {
    if (!loc) { go("locations"); return }
    selected = loc
    if (loc.provider === "surfshark" && !surfsharkReady) { go("accounts"); return }
    run(loc.id, ["connect", loc.provider, loc.id])
    go("home")
  }
  function disconnect() { run("down", ["down"]) }
  function runLeakTest() {
    if (leakProc.running) return
    leakResult = null
    leakProc.command = [ctl, "leaktest"]
    leakProc.running = true
  }
  function copy(text) { root.bar.run("wl-copy " + root.bar.shellQuote(text)); message = "Copied to the clipboard."; messageIsError = false }
  // A location to try a fresh key with: the last one, else Bogotá, else the least loaded.
  function testLocation() {
    if (target && target.provider === "surfshark") return target
    var all = locs ? locs.surfshark : []
    var bog = all.filter(function(l) { return l.id.indexOf("co-bog") === 0 })[0]
    if (bog) return bog
    return all.slice().sort(function(a, b) { return (a.load || 100) - (b.load || 100) })[0] || null
  }
  function primary() { if (connected) disconnect(); else connectTo(target) }
  function openApp(bin) { root.bar.run("uwsm-app -- " + bin); root.close() }
  function openUrl(u) { root.bar.run("xdg-open " + u); root.close() }
  function go(name) {
    if (!screens[name]) return
    if (name === "locations") {
      loadLocations(false)
      if (currentLoc && (currentLoc.provider === "surfshark" || currentLoc.provider === "proton")) provider = currentLoc.provider
    }
    screen = name
  }
  function back() { if (screen !== "home") screen = "home"; else root.close() }
  function locLabel(l) {
    if (!l) return ""
    return l.city && l.city !== l.country ? l.city + ", " + l.country : l.country
  }
  function providerLabel(p) { return p === "surfshark" ? "Surfshark" : p === "proton" ? "Proton VPN Free" : "Other VPN" }
  function regionGlyph(r) { return r === "The Americas" ? "🌎" : r === "Asia Pacific" ? "🌏" : "🌍" }
  function duration(since) {
    if (!since) return ""
    var s = Math.max(0, Math.floor(Date.now() / 1000 - since))
    var h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60)
    return h > 0 ? h + " h " + m + " min" : m > 0 ? m + " min" : s + " s"
  }

  onOpenedChanged: {
    if (opened) { refresh(false); loadLocations(false); Qt.callLater(function() { keyCatcher.forceActiveFocus() }) }
    else { message = ""; screen = "home" }
  }
  onScreenChanged: loadScreen()

  Timer {
    interval: root.opened ? 2000 : 20000
    running: true
    repeat: true
    triggeredOnStart: true
    onTriggered: root.refresh(false)
  }

  Process {
    id: statusProc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: { try { root.st = JSON.parse(text) } catch (e) {} }
    }
  }
  Process {
    id: locProc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: { try { root.locs = JSON.parse(text) } catch (e) {} }
    }
  }
  Process {
    id: leakProc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: { try { root.leakResult = JSON.parse(text) } catch (e) { root.leakResult = { ok: false, error: "leak test failed" } } }
    }
  }
  Process {
    id: actionProc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        try {
          var r = JSON.parse(text)
          if (r.ok === false) { root.message = r.error || "failed"; root.messageIsError = true }
          else if (r.publicKey) {
            root.message = r.copied ? "Public key copied. Paste it at my.surfshark.com (step 2)." : "Key created. Copy the public key (step 2)."
            root.messageIsError = false
          }
          else if (r.imported) {
            root.message = "Imported: " + r.imported.map(function(i) { return i.name }).join(", ")
            root.messageIsError = false
            root.loadLocations(false)
          }
        } catch (e) {}
        root.busy = ""
        root.refresh(true)
      }
    }
  }

  IpcHandler {
    target: root.ipcTarget
    function open(): void { root.open() }
    function close(): void { root.close() }
    function toggle(): void { root.toggle() }
    function refresh(): string { root.refresh(true); return "ok" }
    function screen(name: string): void { root.open(); root.go(name) }
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
    text: (root.connected ? root.glyphOn : root.glyphOff)
      + (root.busy !== "" ? " …" : root.currentLoc && root.currentLoc.cc ? " " + root.currentLoc.cc : "")
    active: root.leak
    tooltipText: !root.st ? "VPN"
      : root.connected ? "Protected · " + root.locLabel(root.currentLoc) + " (" + root.providerLabel(root.currentLoc.provider) + ")"
        + (root.ip4 ? "\n" + root.ip4.ip : "") + (root.leak ? "\nIPv6 is leaking around the tunnel" : "")
      : "Not protected" + (root.ip4 ? " · " + root.ip4.country : "")
        + (root.target ? "\nRight click: connect to " + root.locLabel(root.target) : "")
    onPressed: function(code) {
      if (code === Qt.RightButton) root.run("toggle", ["toggle"])
      else root.toggle()
    }
  }

  // ------------------------------------------------------------------ panel
  function loadScreen() {
    flick.contentY = 0
    loader.setSource(Qt.resolvedUrl("screens/" + screens[screen].file + ".qml"), { vpn: root })
  }
  Component.onCompleted: loadScreen()

  KeyboardPanel {
    id: panel
    anchorItem: button
    owner: root
    bar: root.bar
    open: root.opened
    focusTarget: keyCatcher
    contentWidth: panel.fittedContentWidth(Style.space(420))
    contentHeight: panel.fittedContentHeight(header.implicitHeight + Style.space(12) + (loader.item ? loader.item.implicitHeight : 0), Style.space(760))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      onMoveRequested: function(dx, dy) {
        if (dx < 0) root.back()
        if (dy !== 0) flick.contentY = Math.max(0, Math.min(flick.contentHeight - flick.height, flick.contentY + dy * Style.space(56)))
      }
      onCloseRequested: root.back()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onTextKey: function(t) {
        if (root.screen === "locations" && loader.item && loader.item.typeText) { loader.item.typeText(t); return }
        if (t === "r" || t === "R") root.refresh(true)
        else if (t === "c" || t === "C") root.primary()
        else if (t === "l" || t === "L") root.go("locations")
        else if (t === "i" || t === "I") root.go("details")
        else if (t === "a" || t === "A") root.go("accounts")
      }

      // Header: back arrow on inner screens, title, refresh.
      Column {
        id: header
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.top: parent.top
        spacing: Style.space(8)

        RowLayout {
          width: parent.width
          spacing: Style.space(6)
          PanelActionButton {
            visible: root.screen !== "home"
            iconText: "󰁍"
            tooltipText: "Back (Esc)"
            foreground: root.foreground
            fontFamily: root.fontFamily
            onClicked: root.back()
          }
          Text {
            visible: root.screen === "home"
            text: root.connected ? root.glyphOn : root.glyphOff
            color: root.leak ? root.urgent : root.foreground
            font.family: root.fontFamily
            font.pixelSize: Style.font.icon
          }
          Text {
            Layout.fillWidth: true
            text: root.screens[root.screen].title
            color: root.foreground
            font.family: root.fontFamily
            font.pixelSize: Style.font.body
            font.bold: true
            elide: Text.ElideRight
          }
          PanelActionButton {
            iconText: "󰑐"
            tooltipText: "Check again (r)"
            foreground: root.foreground
            fontFamily: root.fontFamily
            onClicked: { root.refresh(true); if (root.screen === "locations") root.loadLocations(true) }
          }
        }
        Notice { visible: root.message !== ""; text: root.message; level: root.messageIsError ? "crit" : "info" }
      }

      Flickable {
        id: flick
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.top: header.bottom
        anchors.topMargin: Style.space(12)
        anchors.bottom: parent.bottom
        contentWidth: width
        contentHeight: loader.item ? loader.item.implicitHeight : 0
        clip: true
        boundsBehavior: Flickable.StopAtBounds
        interactive: contentHeight > height
        ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

        Loader {
          id: loader
          // Always leave the scrollbar gutter. Making the width depend on flick.interactive
          // was a loop: scrollbar on -> narrower -> content (photo grid) shorter -> no
          // scrollbar -> wider -> taller -> ... The shell spun at 100 % and its panel kept the
          // keyboard, so nothing could be typed anywhere (2026-10-08 09:40).
          width: flick.width - Style.space(12)
          onLoaded: item.width = Qt.binding(function() { return loader.width })
        }
      }
    }
  }
}
