import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

// iCloud Drive through the rclone mount at ~/iCloud: setup and Apple's 30-day
// sign-in, mount state, documents with their sync state (cloud / on this PC /
// uploading), uploads in flight and queued, cache, recent files, and the
// optional read-only iCloud Photos mount.
Panel {
  id: root
  moduleName: "juansebas7ian.icloud"
  ipcTarget: "juansebas7ian.icloud"
  manageIpc: false

  readonly property string backend: String(Qt.resolvedUrl("icloud.py")).replace("file://", "")
  readonly property string glyph: String.fromCodePoint(0xF0038)
  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property color urgent: bar ? bar.urgent : Color.urgent
  readonly property color dim: Qt.darker(foreground, 1.55)
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family

  property var st: null
  property bool busy: false
  property string dir: ""
  property var listing: null
  readonly property bool configured: !!st && st.configured
  readonly property bool mounted: !!st && st.mounted
  readonly property var cache: st ? (st.cache || {}) : {}
  readonly property int pendingUploads: (cache.uploadsInProgress || 0) + (cache.uploadsQueued || 0)
  readonly property bool syncing: pendingUploads > 0 || (!!st && st.transfers.length > 0)
  readonly property var daysLeft: st && st.auth ? st.auth.daysLeft : null
  readonly property bool authExpired: !!st && ((st.authError || "") !== "" || (daysLeft !== null && daysLeft <= 0))
  readonly property bool authSoon: daysLeft !== null && daysLeft <= 3
  readonly property bool failing: !!st && ((cache.erroredFiles || 0) > 0 || st.service === "failed" || authExpired)

  function bytes(n) {
    n = Number(n || 0)
    if (n >= 1e12) return (n / 1e12).toFixed(2) + " TB"
    if (n >= 1e9) return (n / 1e9).toFixed(1) + " GB"
    if (n >= 1e6) return (n / 1e6).toFixed(1) + " MB"
    if (n >= 1e3) return (n / 1e3).toFixed(0) + " KB"
    return n + " B"
  }
  function ago(t) {
    var s = Math.max(0, Date.now() / 1000 - t)
    if (s < 60) return "just now"
    if (s < 3600) return Math.round(s / 60) + " min ago"
    if (s < 86400) return Math.round(s / 3600) + " h ago"
    return Math.round(s / 86400) + " d ago"
  }
  function stateGlyph(s) {
    return { folder: "󰉋", cloud: "☁", partial: "◐", local: "✓", queued: "⏳", uploading: "↑" }[s] || "·"
  }
  function stateText(s) {
    return { cloud: "in iCloud", partial: "partly on this PC", local: "on this PC",
             queued: "waiting to upload", uploading: "uploading" }[s] || ""
  }

  function refresh() { if (!statusProc.running) statusProc.running = true }
  function list(d) {
    if (d !== undefined) dir = d
    if (lsProc.running) return
    lsProc.command = ["python3", backend, "ls", dir]
    lsProc.running = true
  }
  function up() { list(dir.indexOf("/") < 0 ? "" : dir.substring(0, dir.lastIndexOf("/"))) }
  function act(cmd, arg) {
    if (actionProc.running) return
    busy = true
    actionProc.command = arg === undefined ? ["python3", backend, cmd] : ["python3", backend, cmd, arg]
    actionProc.running = true
  }
  function terminal(args) {
    root.bar.run("uwsm-app -- xdg-terminal-exec --title=iCloud " + args)
    root.close()
  }
  function openPath(rel) {
    var p = st.mountpoint + (rel ? "/" + rel : "")
    root.bar.run("xdg-open " + root.bar.shellQuote(p))
    root.close()
  }

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  onOpenedChanged: if (opened) {
    refresh()
    if (mounted) list()
    Qt.callLater(function() { keyCatcher.forceActiveFocus() })
  }
  onMountedChanged: if (mounted && opened) list()

  // Fast while something is moving or the panel is open, slow otherwise.
  Timer {
    interval: root.opened || root.syncing ? 2000 : 15000
    running: true
    repeat: true
    triggeredOnStart: true
    onTriggered: root.refresh()
  }
  // The document list (and each file's state) while the panel is open.
  Timer {
    interval: 4000
    running: root.opened && root.mounted
    repeat: true
    onTriggered: root.list()
  }

  Process {
    id: statusProc
    command: ["python3", root.backend, "status"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: { try { root.st = JSON.parse(text) } catch (e) {} }
    }
  }

  Process {
    id: lsProc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: { try { root.listing = JSON.parse(text) } catch (e) {} }
    }
  }

  Process {
    id: actionProc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: { root.busy = false; root.refresh(); root.list() }
    }
  }

  IpcHandler {
    target: root.ipcTarget
    function open(): void { root.open() }
    function close(): void { root.close() }
    function toggle(): void { root.toggle() }
    function refresh(): string { root.refresh(); return "ok" }
  }

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: root.glyph + (root.pendingUploads > 0 ? " ↑" + root.pendingUploads : "")
    dimmed: !root.mounted
    active: root.failing || root.authSoon
    tooltipText: !root.st ? "iCloud Drive"
      : !root.configured ? "iCloud Drive: not set up (click to connect)"
      : root.authExpired ? "iCloud Drive: sign in again"
      : !root.mounted ? "iCloud Drive: not mounted"
      : root.syncing ? "iCloud Drive: syncing " + root.pendingUploads + " file(s)"
      : "iCloud Drive: up to date" + (root.daysLeft !== null ? " · sign-in renews in " + Math.floor(root.daysLeft) + " d" : "")
    onPressed: function(code) {
      if (code === Qt.RightButton && root.mounted) root.openPath("")
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
    contentHeight: panel.fittedContentHeight(column.implicitHeight, Style.space(760))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      onCloseRequested: root.close()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onTextKey: function(t) {
        if (t === "r" || t === "R") root.act("refresh")
        else if ((t === "o" || t === "O") && root.mounted) root.openPath(root.dir)
        else if (t === "b" || t === "B" || t === "\b") root.up()
      }

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
            id: hero
            width: parent.width
            title: "iCloud Drive"
            meta: !root.st ? "" : root.busy ? "Working…"
              : !root.configured ? "Not connected"
              : root.authExpired ? "Sign-in expired"
              : !root.mounted ? "Not mounted"
              : root.syncing ? "Syncing " + root.pendingUploads + " file(s)"
              : "Up to date · ~/iCloud"
            foreground: root.foreground
            fontFamily: root.fontFamily
            iconOpacity: root.mounted ? 1.0 : 0.5
            iconComponent: Component {
              Text { text: root.glyph; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.display }
            }
            trailingControl: Component {
              ToggleSwitch {
                checked: root.mounted
                busy: root.busy
                enabled: root.configured
                foreground: hero.foreground
                onToggled: root.act(root.mounted ? "unmount" : "mount")
              }
            }
          }

          // Not set up yet
          Column {
            visible: !!root.st && !root.configured
            width: parent.width
            spacing: Style.space(6)
            Text {
              width: parent.width
              text: "Connect your Apple ID to sync iCloud Drive to ~/iCloud. Before that, on the iPhone turn on "
                + "Settings › [your name] › iCloud › “Access iCloud Data on the Web”. You will need your password and a 2FA code."
              color: root.foreground
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
              wrapMode: Text.WordWrap
            }
            PanelActionButton {
              iconText: "󰌾"
              tooltipText: "Connect iCloud (opens a terminal)"
              foreground: root.foreground
              fontFamily: root.fontFamily
              onClicked: root.terminal("icloud-setup")
            }
          }

          // Sign-in and errors
          Text {
            visible: !!root.st && root.configured && (root.failing || root.authSoon || (root.st.lastError || "") !== "")
            width: parent.width
            text: !root.st ? "" : root.authExpired
                ? "Apple's sign-in expired (it lasts 30 days). Press the key button below and type the 2FA code."
                  + (root.st.authError ? "\n" + root.st.authError : "")
              : root.authSoon ? "Apple's sign-in expires in " + Math.max(0, Math.floor(root.daysLeft)) + " day(s): renew it with the key button."
              : ((root.cache.erroredFiles || 0) > 0 ? root.cache.erroredFiles + " file(s) failed to upload. " : "")
                + (root.st.service === "failed" ? "The mount service failed: journalctl --user -u rclone-icloud" : (root.st.lastError || ""))
            color: root.urgent
            font.family: root.fontFamily
            font.pixelSize: Style.font.bodySmall
            wrapMode: Text.WordWrap
          }

          // Storage and session
          Column {
            visible: root.configured
            width: parent.width
            spacing: Style.space(4)
            Pair {
              visible: !!root.st && !!root.st.quota && !!root.st.quota.total
              label: "iCloud storage"
              value: root.st && root.st.quota ? root.bytes(root.st.quota.used) + " of " + root.bytes(root.st.quota.total) : ""
            }
            Pair {
              label: "Sign-in"
              value: root.daysLeft === null ? "date unknown (renew to start the count)"
                : root.daysLeft <= 0 ? "expired" : "renews in " + Math.floor(root.daysLeft) + " d"
            }
            Pair {
              visible: root.mounted
              label: "On this PC (cache)"
              value: root.bytes(root.cache.bytesUsed) + " · " + (root.cache.files || 0) + " files"
            }
            Pair {
              label: "iCloud Photos (read-only)"
              value: !root.st ? "" : root.st.photos.mounted ? "~/iCloudPhotos" : "off"
            }
          }

          // Activity
          Column {
            visible: root.mounted
            width: parent.width
            spacing: Style.space(6)
            PanelSeparator { foreground: root.foreground }
            PanelSectionHeader { text: "ACTIVITY"; foreground: root.foreground; fontFamily: root.fontFamily }
            Text {
              visible: !root.syncing
              text: "Nothing uploading or downloading."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
            }
            Repeater {
              model: root.st ? root.st.transfers : []
              Column {
                required property var modelData
                width: column.width
                spacing: Style.space(2)
                Pair {
                  label: modelData.name
                  value: modelData.percent + "% · " + root.bytes(modelData.speed) + "/s"
                }
                Item {
                  width: parent.width
                  height: Style.space(4)
                  Rectangle { anchors.fill: parent; radius: height / 2; color: Qt.alpha(root.foreground, 0.15) }
                  Rectangle { height: parent.height; radius: height / 2; color: root.foreground; width: parent.width * modelData.percent / 100 }
                }
              }
            }
            Repeater {
              model: root.st ? root.st.queue : []
              Pair {
                required property var modelData
                label: (modelData.uploading ? "↑ " : "⏳ ") + modelData.name
                value: root.bytes(modelData.size) + (modelData.tries > 0 ? " · retry " + modelData.tries : "")
              }
            }
          }

          // Documents
          Column {
            visible: root.mounted
            width: parent.width
            spacing: Style.space(4)
            PanelSeparator { foreground: root.foreground }
            Item {
              width: parent.width
              implicitHeight: docHeader.implicitHeight
              PanelSectionHeader {
                id: docHeader
                anchors.left: parent.left
                text: "DOCUMENTS · " + (root.dir === "" ? "iCloud Drive" : root.dir)
                foreground: root.foreground
                fontFamily: root.fontFamily
              }
              Text {
                anchors.right: parent.right
                anchors.verticalCenter: docHeader.verticalCenter
                visible: root.dir !== ""
                text: "← back"
                color: root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
                MouseArea { anchors.fill: parent; cursorShape: Qt.PointingHandCursor; onClicked: root.up() }
              }
            }
            Text {
              width: parent.width
              visible: !!root.listing && root.listing.ok
              text: {
                if (!root.listing || !root.listing.counts) return ""
                var c = root.listing.counts, parts = []
                if (c.folder) parts.push(c.folder + " folders")
                if (c.local) parts.push(c.local + " on this PC")
                if (c.partial) parts.push(c.partial + " partly")
                if (c.cloud) parts.push(c.cloud + " only in iCloud")
                if (c.queued || c.uploading) parts.push(((c.queued || 0) + (c.uploading || 0)) + " to upload")
                return parts.length ? parts.join(" · ") : "Empty folder"
              }
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
              wrapMode: Text.WordWrap
            }
            Text {
              width: parent.width
              visible: !root.listing || (!root.listing.ok && root.listing.error !== "not mounted")
              text: !root.listing ? "Loading…" : "Could not read the folder: " + root.listing.error
              color: !root.listing ? root.dim : root.urgent
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
              wrapMode: Text.WordWrap
            }
            Repeater {
              model: root.listing && root.listing.ok ? root.listing.entries : []
              CursorSurface {
                id: docRow
                required property var modelData
                width: column.width
                foreground: root.foreground
                hasCursor: docMouse.containsMouse || keepMouse.containsMouse
                implicitHeight: docCol.implicitHeight + Style.space(8)
                MouseArea {
                  id: docMouse
                  anchors.fill: parent
                  hoverEnabled: true
                  cursorShape: Qt.PointingHandCursor
                  onClicked: docRow.modelData.dir ? root.list(docRow.modelData.path) : root.openPath(docRow.modelData.path)
                }
                Text {
                  id: docIcon
                  anchors.left: parent.left
                  anchors.leftMargin: Style.space(8)
                  anchors.verticalCenter: parent.verticalCenter
                  width: Style.space(18)
                  text: root.stateGlyph(docRow.modelData.state)
                  color: docRow.modelData.state === "queued" || docRow.modelData.state === "uploading" ? root.urgent : root.foreground
                  opacity: docRow.modelData.state === "cloud" ? 0.6 : 1.0
                  font.family: root.fontFamily
                  font.pixelSize: Style.font.bodySmall
                }
                Column {
                  id: docCol
                  anchors.left: docIcon.right
                  anchors.right: keep.left
                  anchors.verticalCenter: parent.verticalCenter
                  anchors.rightMargin: Style.space(6)
                  Text {
                    width: parent.width
                    text: docRow.modelData.name
                    color: root.foreground
                    font.family: root.fontFamily
                    font.pixelSize: Style.font.bodySmall
                    elide: Text.ElideMiddle
                  }
                  Text {
                    width: parent.width
                    text: docRow.modelData.dir ? "folder · " + root.ago(docRow.modelData.mtime)
                      : root.bytes(docRow.modelData.size) + " · " + root.ago(docRow.modelData.mtime) + " · " + root.stateText(docRow.modelData.state)
                    color: root.dim
                    font.family: root.fontFamily
                    font.pixelSize: Style.font.caption
                    elide: Text.ElideRight
                  }
                }
                // Download to this PC (folders: everything inside).
                Text {
                  id: keep
                  anchors.right: parent.right
                  anchors.rightMargin: Style.space(8)
                  anchors.verticalCenter: parent.verticalCenter
                  visible: docRow.modelData.dir || docRow.modelData.state === "cloud" || docRow.modelData.state === "partial"
                  width: visible ? implicitWidth : 0
                  text: "󰇚"
                  color: root.foreground
                  opacity: keepMouse.containsMouse ? 1.0 : 0.6
                  font.family: root.fontFamily
                  font.pixelSize: Style.font.body
                  MouseArea {
                    id: keepMouse
                    anchors.fill: parent
                    anchors.margins: -Style.space(4)
                    hoverEnabled: true
                    cursorShape: Qt.PointingHandCursor
                    onClicked: root.act("keep", docRow.modelData.path)
                  }
                }
              }
            }
          }

          // Recent
          Column {
            visible: !!root.st && root.st.recent.length > 0
            width: parent.width
            spacing: Style.space(4)
            PanelSeparator { foreground: root.foreground }
            PanelSectionHeader { text: "RECENT ON THIS PC"; foreground: root.foreground; fontFamily: root.fontFamily }
            Repeater {
              model: root.st ? root.st.recent : []
              CursorSurface {
                id: fileRow
                required property var modelData
                width: column.width
                foreground: root.foreground
                hasCursor: fileMouse.containsMouse
                implicitHeight: fileCol.implicitHeight + Style.space(8)
                MouseArea {
                  id: fileMouse
                  anchors.fill: parent
                  hoverEnabled: true
                  cursorShape: Qt.PointingHandCursor
                  onClicked: root.openPath(fileRow.modelData.path)
                }
                Column {
                  id: fileCol
                  anchors.left: parent.left
                  anchors.right: parent.right
                  anchors.verticalCenter: parent.verticalCenter
                  anchors.leftMargin: Style.space(8)
                  anchors.rightMargin: Style.space(8)
                  Text {
                    width: parent.width
                    text: fileRow.modelData.name
                    color: root.foreground
                    font.family: root.fontFamily
                    font.pixelSize: Style.font.bodySmall
                    elide: Text.ElideMiddle
                  }
                  Text {
                    width: parent.width
                    text: (fileRow.modelData.dir || "/") + " · " + root.bytes(fileRow.modelData.size) + " · " + root.ago(fileRow.modelData.mtime)
                    color: root.dim
                    font.family: root.fontFamily
                    font.pixelSize: Style.font.caption
                    elide: Text.ElideMiddle
                  }
                }
              }
            }
          }

          // Actions
          Row {
            visible: root.configured
            spacing: Style.space(6)
            PanelActionButton {
              iconText: "󰉋"
              tooltipText: "Open this folder (o)"
              foreground: root.foreground
              fontFamily: root.fontFamily
              enabled: root.mounted
              onClicked: root.openPath(root.dir)
            }
            PanelActionButton {
              iconText: "󰑐"
              tooltipText: "Re-read folders from iCloud (r)"
              foreground: root.foreground
              fontFamily: root.fontFamily
              enabled: root.mounted
              onClicked: root.act("refresh")
            }
            PanelActionButton {
              iconText: "󰌾"
              tooltipText: "Renew Apple sign-in (2FA, opens a terminal)"
              foreground: root.foreground
              fontFamily: root.fontFamily
              onClicked: root.terminal("icloud-setup reconnect")
            }
            PanelActionButton {
              iconText: "󰉏"
              tooltipText: root.st && root.st.photos.mounted ? "Unmount iCloud Photos" : "Mount iCloud Photos (read-only) at ~/iCloudPhotos"
              foreground: root.foreground
              fontFamily: root.fontFamily
              onClicked: root.act(root.st && root.st.photos.mounted ? "photos-unmount" : "photos-mount")
            }
            PanelActionButton {
              iconText: "󰖟"
              tooltipText: "Open icloud.com"
              foreground: root.foreground
              fontFamily: root.fontFamily
              onClicked: { root.bar.run("xdg-open https://www.icloud.com/iclouddrive/"); root.close() }
            }
          }
        }
      }
    }
  }

  component Pair: Item {
    property string label: ""
    property string value: ""
    width: parent ? parent.width : 0
    implicitHeight: visible ? Math.max(pl.implicitHeight, pv.implicitHeight) : 0
    Text {
      id: pl
      anchors.left: parent.left
      anchors.right: pv.left
      anchors.rightMargin: Style.space(8)
      text: parent.label
      color: root.foreground
      opacity: 0.6
      font.family: root.fontFamily
      font.pixelSize: Style.font.bodySmall
      elide: Text.ElideMiddle
    }
    Text {
      id: pv
      anchors.right: parent.right
      text: parent.value
      color: root.foreground
      font.family: root.fontFamily
      font.pixelSize: Style.font.bodySmall
    }
  }
}
