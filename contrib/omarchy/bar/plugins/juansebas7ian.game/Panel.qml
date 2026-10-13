import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

// Game mode in one click: background uploads, backups and indexing are frozen while playing (no CPU, disk or
// upload bandwidth, progress kept) and resume afterwards. GameMode switches it by itself for games launched with
// gamemoderun. Backend: omarchy-game (Go).
Panel {
  id: root
  moduleName: "juansebas7ian.game"
  ipcTarget: "juansebas7ian.game"
  manageIpc: false

  readonly property string ctl: Quickshell.env("HOME") + "/.local/bin/omarchy-game"
  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property color dim: Qt.darker(foreground, 1.55)
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family
  property var info: null
  readonly property bool on: !!info && !!info.state && info.state.on
  property string message: ""

  function refresh() { if (!statusProcess.running) statusProcess.running = true }
  function toggleMode() { if (!toggleProcess.running) toggleProcess.running = true }

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight
  onOpenedChanged: if (opened) refresh()

  Timer { interval: root.opened ? 3000 : 15000; running: true; repeat: true; triggeredOnStart: true; onTriggered: root.refresh() }
  Process {
    id: statusProcess
    command: [root.ctl, "status"]
    stdout: StdioCollector { waitForEnd: true; onStreamFinished: { try { root.info = JSON.parse(text) } catch (e) {} } }
  }
  Process {
    id: toggleProcess
    command: [root.ctl, "toggle"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        try { var r = JSON.parse(text.trim().split("\n").pop()); root.message = r.ok === false ? (r.error || "failed") : "" } catch (e) {}
        root.refresh()
      }
    }
  }

  IpcHandler {
    target: root.ipcTarget
    function toggle(): void { root.toggleMode() }
    function open(): void { root.open() }
    function close(): void { root.close() }
  }

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: "󰊴"
    dimmed: !root.on
    tooltipText: root.on ? "Game mode on" + (root.info.state.auto ? " (a game is running)" : "") + " · " + (root.info.state.frozen || []).length + " background jobs paused · click to switch off"
      : "Game mode off · click to pause background uploads, backups and indexing while you play"
    onPressed: function(code) {
      if (code === Qt.RightButton) root.toggle()
      else root.toggleMode()
    }
  }

  KeyboardPanel {
    id: panel
    anchorItem: button
    owner: root
    bar: root.bar
    open: root.opened
    focusTarget: keyCatcher
    contentWidth: panel.fittedContentWidth(Style.space(380))
    contentHeight: panel.fittedContentHeight(column.implicitHeight, Style.space(520))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      onCloseRequested: root.close()
      onTabRequested: function(direction) { root.switchPanel(direction) }

      Column {
        id: column
        width: parent.width - Style.space(12)
        spacing: Style.space(8)
        PanelHero {
          width: parent.width
          title: "Game mode"
          meta: root.on ? (root.info.state.auto ? "On · switched on by the game" : "On · switched on by hand") : "Off"
          foreground: root.foreground
          fontFamily: root.fontFamily
          iconComponent: Component { Text { text: "󰊴"; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.display } }
          trailingControl: Component {
            PanelActionButton { iconText: root.on ? "󰓛" : "󰐊"; tooltipText: root.on ? "Switch off" : "Switch on"; foreground: root.foreground; fontFamily: root.fontFamily; onClicked: root.toggleMode() }
          }
        }
        Text { visible: root.message !== ""; width: parent.width; text: root.message; color: bar ? bar.urgent : Color.urgent; font.family: root.fontFamily; font.pixelSize: Style.font.bodySmall; wrapMode: Text.WordWrap }
        Text {
          width: parent.width
          text: "CPU waiting: " + (root.info ? root.info.cpuPressure.toFixed(1) + "%" : "—") + " · upload: " + (root.info ? root.info.uploadKBps + " KB/s" : "—")
          color: root.foreground
          font.family: root.fontFamily
          font.pixelSize: Style.font.bodySmall
        }
        PanelSectionHeader { visible: root.on; text: "PAUSED WHILE YOU PLAY"; foreground: root.foreground; fontFamily: root.fontFamily }
        Repeater {
          model: root.on ? (root.info.state.frozen || []).concat(root.info.state.stopped || []) : []
          Text {
            required property var modelData
            width: column.width
            text: "· " + modelData.replace(/\.service$/, "").replace(/\.(timer|path)$/, " (scheduler)")
            color: root.dim
            font.family: root.fontFamily
            font.pixelSize: Style.font.caption
            elide: Text.ElideRight
          }
        }
        Text {
          width: parent.width
          text: "Paused: device backups and their Google Drive uploads, Google Photos uploads, the daily backup and the file indexer. They resume where they were. Games launched with gamemoderun switch this on and off by themselves; Proton's Xalia helper is disabled."
          color: root.dim
          font.family: root.fontFamily
          font.pixelSize: Style.font.caption
          wrapMode: Text.WordWrap
        }
      }
    }
  }
}
