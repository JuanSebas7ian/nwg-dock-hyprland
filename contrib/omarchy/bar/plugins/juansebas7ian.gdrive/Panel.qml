import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

// Google Drive through the rclone mount at ~/GoogleDrive: mount state,
// uploads in flight and queued, storage quota, and files touched lately.
Panel {
  id: root
  moduleName: "juansebas7ian.gdrive"
  ipcTarget: "juansebas7ian.gdrive"
  manageIpc: false

  readonly property string backend: String(Qt.resolvedUrl("gdrive.py")).replace("file://", "")
  readonly property string glyph: String.fromCodePoint(0xF02B6)
  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property color urgent: bar ? bar.urgent : Color.urgent
  readonly property color dim: Qt.darker(foreground, 1.55)
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family

  property var st: null
  property bool busy: false
  readonly property bool mounted: !!st && st.mounted
  readonly property var cache: st ? (st.cache || {}) : {}
  readonly property int pendingUploads: (cache.uploadsInProgress || 0) + (cache.uploadsQueued || 0)
  readonly property bool syncing: pendingUploads > 0 || (!!st && st.transfers.length > 0)
  readonly property bool failing: !!st && ((cache.erroredFiles || 0) > 0 || st.service === "failed")

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

  function refresh() { if (!statusProc.running) statusProc.running = true }
  function act(cmd) {
    if (actionProc.running) return
    busy = true
    actionProc.command = ["python3", backend, cmd]
    actionProc.running = true
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
    Qt.callLater(function() { keyCatcher.forceActiveFocus() })
  }

  // Fast while something is moving or the panel is open, slow otherwise.
  Timer {
    interval: root.opened || root.syncing ? 2000 : 15000
    running: true
    repeat: true
    triggeredOnStart: true
    onTriggered: root.refresh()
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
    id: actionProc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: { root.busy = false; root.refresh() }
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
    active: root.failing
    tooltipText: !root.st ? "Google Drive"
      : !root.mounted ? "Google Drive: not mounted"
      : root.syncing ? "Google Drive: syncing " + root.pendingUploads + " file(s)"
      : "Google Drive: up to date" + (root.st.quota ? " · " + root.bytes(root.st.quota.free) + " free" : "")
    onPressed: function(code) {
      if (code === Qt.RightButton) root.openPath("")
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
    contentHeight: panel.fittedContentHeight(column.implicitHeight, Style.space(700))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      onCloseRequested: root.close()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onTextKey: function(t) {
        if (t === "r" || t === "R") root.act("refresh")
        else if (t === "o" || t === "O") root.openPath("")
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
            title: "Google Drive"
            meta: !root.st ? "" : root.busy ? "Working…"
              : !root.mounted ? "Not mounted"
              : root.syncing ? "Syncing " + root.pendingUploads + " file(s)"
              : "Up to date · ~/GoogleDrive"
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
                foreground: hero.foreground
                onToggled: root.act(root.mounted ? "unmount" : "mount")
              }
            }
          }

          Text {
            visible: !!root.st && (root.failing || (root.st.lastError || "") !== "")
            width: parent.width
            text: root.st ? ((root.cache.erroredFiles || 0) > 0 ? root.cache.erroredFiles + " file(s) failed to upload. " : "")
              + (root.st.service === "failed" ? "The mount service failed: journalctl --user -u rclone-gdrive" : (root.st.lastError || "")) : ""
            color: root.urgent
            font.family: root.fontFamily
            font.pixelSize: Style.font.bodySmall
            wrapMode: Text.WordWrap
          }

          // Storage
          Column {
            visible: !!root.st && !!root.st.quota
            width: parent.width
            spacing: Style.space(4)
            Pair {
              label: "Used"
              value: root.st && root.st.quota ? root.bytes(root.st.quota.used + (root.st.quota.other || 0)) + " of " + root.bytes(root.st.quota.total) : ""
            }
            Item {
              width: parent.width
              height: Style.space(5)
              Rectangle { anchors.fill: parent; radius: height / 2; color: Qt.alpha(root.foreground, 0.15) }
              Rectangle {
                height: parent.height
                radius: height / 2
                color: root.foreground
                width: root.st && root.st.quota && root.st.quota.total > 0
                  ? parent.width * Math.min(1, (root.st.quota.used + (root.st.quota.other || 0)) / root.st.quota.total) : 0
              }
            }
            Pair {
              label: "Drive · Gmail/Photos · trash"
              value: root.st && root.st.quota ? root.bytes(root.st.quota.used) + " · " + root.bytes(root.st.quota.other) + " · " + root.bytes(root.st.quota.trashed) : ""
            }
            Pair {
              visible: root.mounted
              label: "Local cache"
              value: root.bytes(root.cache.bytesUsed) + " · " + (root.cache.files || 0) + " files"
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
            spacing: Style.space(6)
            PanelActionButton {
              iconText: "󰉋"
              tooltipText: "Open ~/GoogleDrive (o)"
              foreground: root.foreground
              fontFamily: root.fontFamily
              enabled: root.mounted
              onClicked: root.openPath("")
            }
            PanelActionButton {
              iconText: "󰑐"
              tooltipText: "Re-read folders from Drive (r)"
              foreground: root.foreground
              fontFamily: root.fontFamily
              enabled: root.mounted
              onClicked: root.act("refresh")
            }
            PanelActionButton {
              iconText: "󰖟"
              tooltipText: "Open drive.google.com"
              foreground: root.foreground
              fontFamily: root.fontFamily
              onClicked: { root.bar.run("xdg-open https://drive.google.com"); root.close() }
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
    implicitHeight: Math.max(pl.implicitHeight, pv.implicitHeight)
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
