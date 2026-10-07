import QtQuick
import QtQuick.Controls
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui
import "ui"

// Cloud: one bar icon and one tabbed panel for Google Drive, iCloud Drive,
// Google Photos and Dropbox. backend/cloud.py reads all four in one process;
// actions go to each service's own CLI. Each tab is tabs/<Name>Tab.qml and
// gets this object as `cloud`.
Panel {
  id: root
  moduleName: "juansebas7ian.cloud"
  ipcTarget: "juansebas7ian.cloud"
  manageIpc: false

  readonly property string python: "/usr/bin/python3"
  readonly property string dir: String(Qt.resolvedUrl("backend/")).replace("file://", "")
  readonly property string gphotosSync: Quickshell.env("HOME") + "/.local/bin/gphotos-sync"
  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property color urgent: bar ? bar.urgent : Color.urgent
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family

  Binding { target: Theme; property: "foreground"; value: root.foreground }
  Binding { target: Theme; property: "urgent"; value: root.urgent }
  Binding { target: Theme; property: "fontFamily"; value: root.fontFamily }

  readonly property string glyphDrive: String.fromCodePoint(0xF02B6)
  readonly property string glyphICloud: String.fromCodePoint(0xF0038)
  readonly property string glyphPhotos: String.fromCodePoint(0xF02F9)
  readonly property string glyphDropbox: String.fromCodePoint(0xF01E5)

  // ------------------------------------------------------------------ state
  property string tab: "overview"
  readonly property var tabFiles: ({ overview: "OverviewTab", drive: "DriveTab", icloud: "ICloudTab", photos: "PhotosTab", dropbox: "DropboxTab" })
  readonly property var tabOrder: ["overview", "drive", "icloud", "photos", "dropbox"]
  property var st: null
  property string busy: ""          // service whose action is running
  property string message: ""
  property string icDir: ""
  property var icListing: null

  // Google Drive
  readonly property var gd: st ? st.gdrive : null
  readonly property var gdCache: gd ? (gd.cache || {}) : {}
  readonly property bool gdMounted: !!gd && !!gd.mounted
  readonly property int gdPending: (gdCache.uploadsInProgress || 0) + (gdCache.uploadsQueued || 0)
  readonly property bool gdSyncing: gdPending > 0 || (!!gd && (gd.transfers || []).length > 0)
  readonly property bool gdFailing: !!gd && ((gdCache.erroredFiles || 0) > 0 || gd.service === "failed" || !!gd.error)
  // iCloud
  readonly property var ic: st ? st.icloud : null
  readonly property var icCache: ic ? (ic.cache || {}) : {}
  readonly property bool icConfigured: !!ic && !!ic.configured
  readonly property bool icMounted: !!ic && !!ic.mounted
  readonly property int icPending: (icCache.uploadsInProgress || 0) + (icCache.uploadsQueued || 0)
  readonly property bool icSyncing: icPending > 0 || (!!ic && (ic.transfers || []).length > 0)
  readonly property var icDaysLeft: ic && ic.auth ? ic.auth.daysLeft : null
  readonly property bool icAuthExpired: !!ic && ((ic.authError || "") !== "" || (icDaysLeft !== null && icDaysLeft <= 0))
  readonly property bool icAuthSoon: icDaysLeft !== null && icDaysLeft <= 3
  readonly property bool icFailing: !!ic && ((icCache.erroredFiles || 0) > 0 || ic.service === "failed" || icAuthExpired || !!ic.error)
  // Google Photos
  readonly property var gp: st ? st.gphotos : null
  readonly property string gpState: gp ? (gp.running ? "running" : (gp.state || "idle")) : ""
  readonly property bool gpRunning: gpState === "running"
  readonly property bool gpPaused: !!gp && !!gp.paused
  readonly property bool gpNeedsSetup: gpState === "setup" || gpState === "auth" || gpState === "missing"
  readonly property bool gpFailing: gpState === "auth" || gpState === "error"
  readonly property int gpPending: gp ? (gpRunning ? Math.max(0, (gp.filesTotal || 0) - (gp.filesDone || 0)) : (gp.pendingTotal || 0)) : 0
  // Dropbox
  readonly property var db: st ? st.dropbox : null
  readonly property bool dbRunning: !!db && !!db.running

  readonly property int uploads: gdPending + icPending + (gpPaused || gpNeedsSetup ? 0 : gpPending)
  readonly property bool syncing: gdSyncing || icSyncing || gpRunning || (!!db && !!db.syncing)
  readonly property bool failing: gdFailing || icFailing || gpFailing
  readonly property var issues: {
    var out = []
    if (!st) return out
    if (gdFailing) out.push({ tab: "drive", level: "crit", text: "Google Drive: " + ((gdCache.erroredFiles || 0) > 0 ? gdCache.erroredFiles + " file(s) failed to upload" : gd.service === "failed" ? "the mount service failed" : gd.error) })
    if (icAuthExpired) out.push({ tab: "icloud", level: "crit", text: "iCloud: Apple's sign-in expired. Renew it (2FA code)." })
    else if (icAuthSoon) out.push({ tab: "icloud", level: "warn", text: "iCloud: Apple's sign-in expires in " + Math.max(0, Math.floor(icDaysLeft)) + " day(s)." })
    else if (icFailing) out.push({ tab: "icloud", level: "crit", text: "iCloud: " + ((icCache.erroredFiles || 0) > 0 ? icCache.erroredFiles + " file(s) failed to upload" : "the mount service failed") })
    if (gpState === "auth") out.push({ tab: "photos", level: "crit", text: "Google Photos: Google withdrew the permission. Reconnect." })
    else if (gpState === "error") out.push({ tab: "photos", level: "crit", text: "Google Photos: " + (gp.error || "the last run failed") })
    else if (gpState === "setup") out.push({ tab: "photos", level: "info", text: "Google Photos is not connected yet (gphotos-setup)." })
    if (db && db.installed && !db.running) out.push({ tab: "dropbox", level: "info", text: "Dropbox is paused." })
    return out
  }

  readonly property var tabs: [
    { id: "overview", label: "Overview", badge: uploads > 0 ? "↑" + uploads : "", hot: failing },
    { id: "drive", label: "Drive", badge: gdPending > 0 ? "↑" + gdPending : gdFailing ? "!" : "", hot: gdFailing },
    { id: "icloud", label: "iCloud", badge: icPending > 0 ? "↑" + icPending : (icFailing || icAuthSoon) ? "!" : "", hot: icFailing || icAuthSoon },
    { id: "photos", label: "Photos", badge: gpNeedsSetup ? "!" : gpPending > 0 && !gpPaused ? "↑" + gpPending : "", hot: gpFailing },
    { id: "dropbox", label: "Dropbox", badge: db && db.syncing ? "↻" : "", hot: false }
  ]

  // ------------------------------------------------------------------ actions
  function refresh() { if (!statusProc.running) statusProc.running = true }
  function act(service, args) {
    if (actionProc.running) return
    busy = service
    message = ""
    var cmd = service === "gdrive" ? [python, "-B", dir + "gdrive.py"].concat(args)
      : service === "icloud" ? [python, "-B", dir + "icloud.py"].concat(args)
      : service === "gphotos" ? [gphotosSync].concat(args)
      : ["dropbox-cli"].concat(args)
    actionProc.command = cmd
    actionProc.running = true
  }
  function icList(d) {
    if (d !== undefined) icDir = d
    if (lsProc.running) return
    lsProc.command = [python, "-B", dir + "icloud.py", "ls", icDir]
    lsProc.running = true
  }
  function icUp() { icList(icDir.indexOf("/") < 0 ? "" : icDir.substring(0, icDir.lastIndexOf("/"))) }
  function photosSyncNow() {
    root.bar.run("systemctl --user start --no-block gphotos-sync.service")
    Qt.callLater(root.refresh)
  }
  function terminal(title, args) {
    root.bar.run("uwsm-app -- xdg-terminal-exec --title=" + title + " " + args)
    root.close()
  }
  function openPath(p) { root.bar.run("xdg-open " + root.bar.shellQuote(p)); root.close() }
  function openUrl(u) { root.bar.run("xdg-open " + u); root.close() }
  function setTab(id) { if (tabFiles[id]) tab = id }
  function stepTab(d) {
    var i = tabOrder.indexOf(tab)
    setTab(tabOrder[(i + d + tabOrder.length) % tabOrder.length])
  }
  function refreshTab() {
    if (tab === "drive" && gdMounted) act("gdrive", ["refresh"])
    else if (tab === "icloud" && icMounted) act("icloud", ["refresh"])
    else if (tab === "photos" && !gpNeedsSetup) photosSyncNow()
    else refresh()
  }
  function openTabFolder() {
    if (tab === "drive" && gdMounted) openPath(gd.mountpoint)
    else if (tab === "icloud" && icMounted) openPath(ic.mountpoint + (icDir ? "/" + icDir : ""))
    else if (tab === "photos") openPath(Quickshell.env("HOME") + "/GoogleFotos")
    else if (tab === "dropbox" && db && db.accountPath) openPath(db.accountPath)
  }

  onOpenedChanged: {
    if (opened) {
      refresh()
      if (tab === "icloud" && icMounted) icList()
      Qt.callLater(function() { keyCatcher.forceActiveFocus() })
    } else message = ""
  }
  onTabChanged: {
    message = ""
    if (tab === "icloud" && icMounted) icList()
    loadTab()
  }
  onIcMountedChanged: if (icMounted && opened && tab === "icloud") icList()

  // Fast while something moves or the panel is open, slow otherwise.
  Timer {
    interval: root.opened || root.syncing ? 2000 : 15000
    running: true
    repeat: true
    triggeredOnStart: true
    onTriggered: root.refresh()
  }
  Timer {
    interval: 4000
    running: root.opened && root.tab === "icloud" && root.icMounted
    repeat: true
    onTriggered: root.icList()
  }

  Process {
    id: statusProc
    command: [root.python, "-B", root.dir + "cloud.py", "status"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: { try { root.st = JSON.parse(text) } catch (e) {} }
    }
  }
  Process {
    id: lsProc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: { try { root.icListing = JSON.parse(text) } catch (e) {} }
    }
  }
  Process {
    id: actionProc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        try {
          var r = JSON.parse(text)
          if (r && r.ok === false) root.message = root.busy + ": " + (r.error || "failed")
        } catch (e) {}
        root.busy = ""
        root.refresh()
        if (root.tab === "icloud") root.icList()
      }
    }
  }

  IpcHandler {
    target: root.ipcTarget
    function open(): void { root.open() }
    function close(): void { root.close() }
    function toggle(): void { root.toggle() }
    function refresh(): string { root.refresh(); return "ok" }
    function tab(name: string): void { root.setTab(name); root.open() }
  }

  // ------------------------------------------------------------------ bar icon
  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: "󰅟" + (root.uploads > 0 ? " ↑" + root.uploads : root.syncing ? " ↻" : "")
    active: root.failing || root.icAuthSoon
    tooltipText: !root.st ? "Cloud"
      : "Drive: " + (!root.gdMounted ? "not mounted" : root.gdPending > 0 ? "↑" + root.gdPending : "up to date")
        + "\niCloud: " + (!root.icConfigured ? "not connected" : root.icAuthExpired ? "sign in again" : !root.icMounted ? "not mounted"
                         : root.icPending > 0 ? "↑" + root.icPending : "up to date" + (root.icDaysLeft !== null ? " · sign-in " + Math.floor(root.icDaysLeft) + " d" : ""))
        + "\nPhotos: " + (root.gpNeedsSetup ? "not connected" : root.gpPaused ? "paused" : root.gpRunning ? "uploading" : root.gpPending > 0 ? root.gpPending + " pending" : "up to date")
        + "\nDropbox: " + (root.db && root.db.installed ? (root.db.statusText || "") : "not installed")
    onPressed: function(code) {
      if (code === Qt.RightButton) { if (root.gdMounted) root.openPath(root.gd.mountpoint) }
      else root.toggle()
    }
  }

  // ------------------------------------------------------------------ panel
  function loadTab() {
    flick.contentY = 0
    loader.setSource(Qt.resolvedUrl("tabs/" + tabFiles[tab] + ".qml"), { cloud: root })
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
    contentHeight: panel.fittedContentHeight(header.implicitHeight + Style.space(10) + (loader.item ? loader.item.implicitHeight : 0), Style.space(1000))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      onMoveRequested: function(dx, dy) {
        if (dx !== 0) root.stepTab(dx)
        if (dy !== 0) flick.contentY = Math.max(0, Math.min(flick.contentHeight - flick.height, flick.contentY + dy * Style.space(56)))
      }
      onCloseRequested: (root.tab === "icloud" && root.icDir !== "") ? root.icUp() : root.close()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onTextKey: function(t) {
        if (t === "r" || t === "R") root.refreshTab()
        else if (t === "o" || t === "O") root.openTabFolder()
        else if (t >= "1" && t <= "5") root.setTab(root.tabOrder[Number(t) - 1])
      }

      Column {
        id: header
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.top: parent.top
        spacing: Style.space(10)

        PanelHero {
          width: parent.width
          title: "Cloud"
          meta: !root.st ? "Checking…"
            : root.busy !== "" ? "Working…"
            : root.uploads > 0 ? root.uploads + " file(s) waiting to upload"
            : root.syncing ? "Syncing…"
            : root.failing ? "Needs attention" : "Everything up to date"
          foreground: root.foreground
          fontFamily: root.fontFamily
          iconComponent: Component {
            Text { text: "󰅟"; color: root.failing ? root.urgent : root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.display }
          }
          trailingControl: Component {
            PanelActionButton {
              iconText: "󰑐"
              tooltipText: "Re-read this service (r) · o = open its folder"
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

        Notice { visible: root.message !== ""; text: root.message; level: "crit" }
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
          width: flick.width - (flick.interactive ? Style.space(12) : 0)
          onLoaded: item.width = Qt.binding(function() { return loader.width })
        }
      }
    }
  }
}
