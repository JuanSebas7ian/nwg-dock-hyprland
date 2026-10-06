import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

// Google Photos uploads (gphotos-sync, rclone remote "gphotos:"): what is
// uploading now, how far each source is, and the photos that went up last.
// Google's API cannot read the rest of the library, so this is upload-only.
Panel {
  id: root
  moduleName: "juansebas7ian.gphotos"
  ipcTarget: "juansebas7ian.gphotos"
  manageIpc: false

  readonly property string sync: Quickshell.env("HOME") + "/.local/bin/gphotos-sync"
  readonly property string glyph: String.fromCodePoint(0xF02F9)
  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property color urgent: bar ? bar.urgent : Color.urgent
  readonly property color dim: Qt.darker(foreground, 1.55)
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family

  property var st: null
  property bool busy: false
  readonly property string syncState: st ? (st.running ? "running" : (st.state || "idle")) : ""
  readonly property bool running: syncState === "running"
  readonly property bool paused: !!st && st.paused
  readonly property bool needsSetup: syncState === "setup" || syncState === "auth"
  readonly property bool failing: syncState === "auth" || syncState === "error" || (!!st && (st.error || "") !== "" && state !== "quota" && state !== "setup")
  readonly property int pending: st ? (st.pendingTotal || 0) : 0
  readonly property int remaining: running ? Math.max(0, (st.filesTotal || 0) - (st.filesDone || 0)) : pending

  function bytes(n) {
    n = Number(n || 0)
    if (n >= 1e12) return (n / 1e12).toFixed(2) + " TB"
    if (n >= 1e9) return (n / 1e9).toFixed(1) + " GB"
    if (n >= 1e6) return (n / 1e6).toFixed(1) + " MB"
    if (n >= 1e3) return (n / 1e3).toFixed(0) + " KB"
    return n + " B"
  }
  function duration(s) {
    s = Number(s || 0)
    if (s <= 0) return ""
    if (s < 3600) return Math.max(1, Math.round(s / 60)) + " min"
    return Math.floor(s / 3600) + " h " + Math.round((s % 3600) / 60) + " min"
  }
  function ago(t) {
    var s = Math.max(0, Date.now() / 1000 - t)
    if (s < 60) return "ahora"
    if (s < 3600) return "hace " + Math.round(s / 60) + " min"
    if (s < 86400) return "hace " + Math.round(s / 3600) + " h"
    return "hace " + Math.round(s / 86400) + " d"
  }
  function stateText() {
    if (!st) return ""
    if (busy) return "Trabajando…"
    if (syncState === "setup") return "Sin conectar"
    if (syncState === "auth") return "Google retiró el permiso: reconecta"
    if (syncState === "quota") return "Cuota diaria de Google agotada: sigue más tarde"
    if (paused) return "En pausa" + (pending > 0 ? " · " + pending + " pendientes" : "")
    if (running) return "Subiendo · " + (st.filesDone || 0) + " de " + (st.filesTotal || 0)
    if (pending > 0) return pending + " pendientes · próxima pasada en ≤ 30 min"
    return "Al día" + (st.lastOk ? " · " + ago(st.lastOk) : "")
  }

  function refresh() { if (!statusProc.running) statusProc.running = true }
  function act(args) {
    if (actionProc.running) return
    busy = true
    actionProc.command = [root.sync].concat(args)
    actionProc.running = true
  }
  function syncNow() {
    root.bar.run("systemctl --user start --no-block gphotos-sync.service")
    Qt.callLater(root.refresh)
  }
  function terminal(args) {
    root.bar.run("uwsm-app -- xdg-terminal-exec --title=GoogleFotos " + args)
    root.close()
  }
  function openPath(p) {
    root.bar.run("xdg-open " + root.bar.shellQuote(p))
    root.close()
  }

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  onOpenedChanged: if (opened) {
    refresh()
    Qt.callLater(function() { keyCatcher.forceActiveFocus() })
  }

  Timer {
    interval: root.opened || root.running ? 2000 : 30000
    running: true
    repeat: true
    triggeredOnStart: true
    onTriggered: root.refresh()
  }

  Process {
    id: statusProc
    command: [root.sync, "status"]
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
    text: root.glyph + (root.remaining > 0 && !root.paused && !root.needsSetup ? " ↑" + root.remaining : "")
    dimmed: root.paused || root.needsSetup
    active: root.failing
    tooltipText: "Google Fotos: " + (root.st ? root.stateText() : "…")
    onPressed: function(code) {
      if (code === Qt.RightButton) { root.bar.run("xdg-open https://photos.google.com"); root.close() }
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
    contentHeight: panel.fittedContentHeight(column.implicitHeight, Style.space(720))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      onCloseRequested: root.close()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onTextKey: function(t) {
        if (t === "s" || t === "S") root.syncNow()
        else if (t === "o" || t === "O") root.openPath(Quickshell.env("HOME") + "/GoogleFotos")
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
            title: "Google Fotos"
            meta: root.stateText()
            foreground: root.foreground
            fontFamily: root.fontFamily
            iconOpacity: root.paused || root.needsSetup ? 0.5 : 1.0
            iconComponent: Component {
              Text { text: root.glyph; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.display }
            }
            trailingControl: Component {
              ToggleSwitch {
                visible: !root.needsSetup
                checked: !root.paused
                busy: root.busy
                foreground: hero.foreground
                onToggled: root.act([root.paused ? "resume" : "pause"])
              }
            }
          }

          Text {
            visible: root.syncState === "setup"
            width: parent.width
            text: "Pulsa la llave: abre gphotos-setup, que te guía para crear tu client ID de Google (5 min) y dar el permiso. "
              + "Después se suben solas ~/GoogleFotos, las capturas, las Cargas de cámara de Dropbox y las fotos de Google Drive, "
              + "cada carpeta en su álbum."
            color: root.dim
            font.family: root.fontFamily
            font.pixelSize: Style.font.bodySmall
            wrapMode: Text.WordWrap
          }

          Text {
            visible: !!root.st && (root.st.error || "") !== "" && root.syncState !== "setup"
            width: parent.width
            text: root.st ? (root.st.error || "") : ""
            color: root.syncState === "quota" || root.syncState === "setup" ? root.dim : root.urgent
            font.family: root.fontFamily
            font.pixelSize: Style.font.bodySmall
            wrapMode: Text.WordWrap
          }

          // Uploading now
          Column {
            visible: root.running
            width: parent.width
            spacing: Style.space(4)
            Pair {
              label: root.st && root.st.album ? "Álbum: " + root.st.album : "Preparando…"
              value: root.st ? root.bytes(root.st.bytesDone) + " de " + root.bytes(root.st.bytesTotal) : ""
            }
            Bar { fraction: root.st && root.st.bytesTotal > 0 ? root.st.bytesDone / root.st.bytesTotal : 0 }
            Pair {
              label: root.st ? (root.st.filesDone || 0) + " de " + (root.st.filesTotal || 0) + " archivos" : ""
              value: root.st && root.st.speed > 0 ? root.bytes(root.st.speed) + "/s" + (root.st.eta ? " · faltan " + root.duration(root.st.eta) : "") : ""
            }
            Repeater {
              model: root.st ? (root.st.transferring || []) : []
              Pair {
                required property var modelData
                label: "↑ " + modelData.name
                value: modelData.percent + "%"
              }
            }
          }

          // Sources
          Column {
            visible: !!root.st && !root.needsSetup
            width: parent.width
            spacing: Style.space(6)
            PanelSeparator { foreground: root.foreground }
            PanelSectionHeader { text: "ORÍGENES"; foreground: root.foreground; fontFamily: root.fontFamily }
            Repeater {
              model: root.st ? root.st.sources : []
              Item {
                id: srcRow
                required property var modelData
                width: column.width
                implicitHeight: Math.max(srcCol.implicitHeight, srcSwitch.implicitHeight)
                Column {
                  id: srcCol
                  anchors.left: parent.left
                  anchors.right: srcSwitch.left
                  anchors.rightMargin: Style.space(8)
                  anchors.verticalCenter: parent.verticalCenter
                  Text {
                    width: parent.width
                    text: srcRow.modelData.name
                    color: root.foreground
                    opacity: srcRow.modelData.enabled ? 1 : 0.5
                    font.family: root.fontFamily
                    font.pixelSize: Style.font.bodySmall
                    elide: Text.ElideRight
                  }
                  Text {
                    width: parent.width
                    text: srcRow.modelData.error ? srcRow.modelData.error
                      : srcRow.modelData.total === 0 && !root.st.lastRun ? "Sin revisar todavía"
                      : srcRow.modelData.uploaded + " de " + srcRow.modelData.total + " subidas"
                        + (srcRow.modelData.pending > 0 ? " · faltan " + srcRow.modelData.pending + " (" + root.bytes(srcRow.modelData.pendingBytes) + ")" : " ✓")
                    color: srcRow.modelData.error ? root.urgent : root.dim
                    font.family: root.fontFamily
                    font.pixelSize: Style.font.caption
                    elide: Text.ElideRight
                  }
                }
                ToggleSwitch {
                  id: srcSwitch
                  anchors.right: parent.right
                  anchors.verticalCenter: parent.verticalCenter
                  checked: srcRow.modelData.enabled
                  foreground: root.foreground
                  onToggled: root.act(["source", srcRow.modelData.id, srcRow.modelData.enabled ? "off" : "on"])
                }
              }
            }
          }

          // Recent uploads
          Column {
            visible: !!root.st && (root.st.recent || []).length > 0
            width: parent.width
            spacing: Style.space(6)
            PanelSeparator { foreground: root.foreground }
            PanelSectionHeader { text: "SUBIDAS RECIENTES"; foreground: root.foreground; fontFamily: root.fontFamily }
            Grid {
              id: grid
              columns: 4
              spacing: Style.space(6)
              readonly property real cell: (column.width - spacing * (columns - 1)) / columns
              Repeater {
                model: root.st ? (root.st.recent || []) : []
                CursorSurface {
                  id: tile
                  required property var modelData
                  width: grid.cell
                  height: grid.cell
                  foreground: root.foreground
                  hasCursor: tileMouse.containsMouse
                  Rectangle { anchors.fill: parent; radius: Style.space(4); color: Qt.alpha(root.foreground, 0.08) }
                  Image {
                    anchors.fill: parent
                    anchors.margins: Style.space(2)
                    visible: (tile.modelData.thumb || "") !== ""
                    source: tile.modelData.thumb ? "file://" + tile.modelData.thumb : ""
                    sourceSize.width: 256
                    sourceSize.height: 256
                    fillMode: Image.PreserveAspectCrop
                    asynchronous: true
                    cache: false
                  }
                  Text {
                    anchors.centerIn: parent
                    visible: (tile.modelData.thumb || "") === ""
                    text: tile.modelData.kind === "video" ? "󰕧" : root.glyph
                    color: root.dim
                    font.family: root.fontFamily
                    font.pixelSize: Style.font.display
                  }
                  MouseArea {
                    id: tileMouse
                    anchors.fill: parent
                    hoverEnabled: true
                    cursorShape: tile.modelData.path ? Qt.PointingHandCursor : Qt.ArrowCursor
                    onClicked: if (tile.modelData.path) root.openPath(tile.modelData.path)
                    ToolTip.visible: containsMouse
                    ToolTip.delay: 400
                    ToolTip.text: tile.modelData.rel + "\n→ " + tile.modelData.album + " · " + root.ago(tile.modelData.t)
                  }
                }
              }
            }
          }

          // Actions
          Row {
            spacing: Style.space(6)
            PanelActionButton {
              visible: root.needsSetup
              iconText: "󰌆"
              tooltipText: root.syncState === "auth" ? "Reconnect Google Photos (opens a terminal)" : "Connect Google Photos (opens a terminal)"
              foreground: root.foreground
              fontFamily: root.fontFamily
              onClicked: root.terminal(root.syncState === "auth" ? "gphotos-setup reconnect" : "gphotos-setup")
            }
            PanelActionButton {
              visible: !root.needsSetup
              iconText: "󰑐"
              tooltipText: "Sync now (s)"
              foreground: root.foreground
              fontFamily: root.fontFamily
              enabled: !root.running && !root.paused
              onClicked: root.syncNow()
            }
            PanelActionButton {
              iconText: "󰉋"
              tooltipText: "Open ~/GoogleFotos: anything copied there is uploaded (o)"
              foreground: root.foreground
              fontFamily: root.fontFamily
              onClicked: root.openPath(Quickshell.env("HOME") + "/GoogleFotos")
            }
            PanelActionButton {
              iconText: "󰖟"
              tooltipText: "Open photos.google.com"
              foreground: root.foreground
              fontFamily: root.fontFamily
              onClicked: { root.bar.run("xdg-open https://photos.google.com"); root.close() }
            }
          }
        }
      }
    }
  }

  component Bar: Item {
    property real fraction: 0
    width: parent ? parent.width : 0
    height: Style.space(5)
    Rectangle { anchors.fill: parent; radius: height / 2; color: Qt.alpha(root.foreground, 0.15) }
    Rectangle { height: parent.height; radius: height / 2; color: root.foreground; width: parent.width * Math.max(0, Math.min(1, parent.fraction)) }
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
