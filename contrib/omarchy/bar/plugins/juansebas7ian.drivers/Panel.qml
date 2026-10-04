import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

// Motherboard and drivers: omarchy-hwcheck health, pending driver package
// updates, and the installed BIOS against the newest one ASUS publishes.
Panel {
  id: root
  moduleName: "juansebas7ian.drivers"
  ipcTarget: "juansebas7ian.drivers"
  manageIpc: false

  readonly property string backend: String(Qt.resolvedUrl("drivers.py")).replace("file://", "")
  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property color urgent: bar ? bar.urgent : Color.urgent
  readonly property color dim: Qt.darker(foreground, 1.55)
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family

  property var d: null
  property bool loading: false
  readonly property var counts: d && d.health ? (d.health.counts || {}) : {}
  readonly property int fails: (counts.fail || 0) + (counts.regression || 0)
  readonly property int warns: counts.warn || 0
  readonly property var driverUpdates: d && d.updates ? (d.updates.drivers || []) : []
  readonly property bool biosNewer: !!d && !!d.bios && d.bios.newer === true
  readonly property int pending: driverUpdates.length + (biosNewer ? 1 : 0)

  function refresh(force) {
    if (proc.running) return
    loading = true
    proc.command = force ? ["python3", backend, "--force"] : ["python3", backend]
    proc.running = true
  }

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  onOpenedChanged: if (opened) {
    refresh(false)
    Qt.callLater(function() { keyCatcher.forceActiveFocus() })
  }

  Timer { interval: 1800000; running: true; repeat: true; triggeredOnStart: true; onTriggered: root.refresh(false) }

  Process {
    id: proc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        root.loading = false
        try { root.d = JSON.parse(text) } catch (e) {}
      }
    }
  }

  IpcHandler {
    target: root.ipcTarget
    function open(): void { root.open() }
    function close(): void { root.close() }
    function toggle(): void { root.toggle() }
    function refresh(): string { root.refresh(true); return "ok" }
  }

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: "" + (root.pending > 0 ? " " + root.pending : "")
    active: root.fails > 0
    tooltipText: !root.d ? "Drivers"
      : (root.fails > 0 ? root.fails + " driver problems · " : root.warns > 0 ? root.warns + " warnings · " : "Drivers OK · ")
        + (root.driverUpdates.length > 0 ? root.driverUpdates.length + " driver updates" : "drivers up to date")
        + (root.biosNewer ? " · BIOS " + root.d.bios.version + " available" : "")
    onPressed: function(code) {
      if (code === Qt.RightButton) root.refresh(true)
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
      onTextKey: function(t) { if (t === "r" || t === "R") root.refresh(true) }

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
            title: root.d ? root.d.board.name : "Motherboard"
            meta: root.d ? "BIOS " + root.d.board.bios + " · " + root.d.board.biosDate : (root.loading ? "Checking…" : "")
            foreground: root.foreground
            fontFamily: root.fontFamily
            iconComponent: Component {
              Text { text: ""; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.display }
            }
            trailingControl: Component {
              PanelActionButton {
                iconText: "󰑐"
                tooltipText: "Check again, including the network (r)"
                foreground: root.foreground
                fontFamily: root.fontFamily
                onClicked: root.refresh(true)
              }
            }
          }

          Line {
            visible: root.loading
            text: "Checking drivers, updates and BIOS…"
            color: root.dim
          }

          // ---------------------------------------------------------- BIOS
          Section { title: "BIOS" }
          Line {
            visible: !!root.d && !!root.d.bios
            text: !root.d ? "" : root.d.bios.error ? "Could not check ASUS: " + root.d.bios.error
              : root.biosNewer
                ? "BIOS " + root.d.bios.version + " available (" + root.d.bios.date + ")" + (root.d.bios.beta ? " · ASUS marks it as beta" : "")
                : "Up to date (latest " + root.d.bios.version + ", " + root.d.bios.date + ")"
            color: root.biosNewer ? root.foreground : root.dim
            bold: root.biosNewer
          }
          Line {
            visible: root.biosNewer && root.d.bios.notes !== ""
            text: root.biosNewer ? root.d.bios.notes : ""
            color: root.dim
            small: true
          }
          ActionRow {
            visible: root.biosNewer
            icon: "󰖟"
            label: "Open the ASUS BIOS page"
            hint: "Flash it from the BIOS with EZ Flash, not from Linux"
            onActivated: { root.bar.run("xdg-open " + root.bar.shellQuote(root.d.bios.page)); root.close() }
          }

          // ---------------------------------------------------------- Health
          Section { title: "HEALTH (omarchy-hwcheck)" }
          Line {
            visible: !!root.d
            text: (root.counts.ok || 0) + " OK · " + root.warns + " warnings · " + root.fails + " failures"
            color: root.fails > 0 ? root.urgent : root.foreground
          }
          Repeater {
            model: root.d && root.d.health ? root.d.health.issues : []
            Column {
              required property var modelData
              width: column.width
              spacing: Style.space(2)
              Line {
                text: (modelData.level === "warn" ? "⚠ " : "✖ ") + modelData.id + "  " + modelData.title
                color: modelData.level === "warn" ? root.foreground : root.urgent
              }
              Line {
                visible: modelData.details.length > 0
                text: modelData.details.join("\n")
                color: root.dim
                small: true
              }
            }
          }
          ActionRow {
            icon: ""
            label: "Full report in a terminal"
            hint: "omarchy-hwcheck"
            onActivated: {
              root.bar.run("omarchy-launch-floating-terminal-with-presentation 'omarchy-hwcheck; omarchy-hwcheck diff'")
              root.close()
            }
          }

          // ---------------------------------------------------------- Updates
          Section { title: "DRIVER UPDATES" }
          Line {
            visible: !!root.d && !!root.d.updates
            text: !root.d ? "" : root.d.updates.error ? "Could not check: " + root.d.updates.error
              : root.driverUpdates.length === 0
                ? "Drivers up to date" + (root.d.updates.total > 0 ? " · " + root.d.updates.total + " other updates pending" : "")
                : root.driverUpdates.length + " driver packages · " + root.d.updates.total + " updates in total"
            color: root.driverUpdates.length > 0 ? root.foreground : root.dim
          }
          Repeater {
            model: root.driverUpdates
            Item {
              required property var modelData
              width: column.width
              implicitHeight: pkgName.implicitHeight
              Text {
                id: pkgName
                text: modelData.name
                color: root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
              }
              Text {
                anchors.right: parent.right
                text: modelData.from + " → " + modelData.to
                color: root.dim
                font.family: root.fontFamily
                font.pixelSize: Style.font.caption
              }
            }
          }
          ActionRow {
            visible: !!root.d && !!root.d.updates && root.d.updates.total > 0
            icon: "󰚰"
            label: "Update the system"
            hint: "omarchy update (validates drivers before rebooting)"
            onActivated: { root.bar.run("omarchy-launch-floating-terminal-with-presentation omarchy-update"); root.close() }
          }

          // ---------------------------------------------------------- Loaded
          Section { title: "LOADED DRIVERS" }
          Repeater {
            model: root.d ? root.d.drivers : []
            Item {
              required property var modelData
              width: column.width
              implicitHeight: drvLabel.implicitHeight
              Text {
                id: drvLabel
                text: modelData.label
                color: root.foreground
                opacity: 0.6
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
              }
              Text {
                anchors.right: parent.right
                text: modelData.version
                color: root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
              }
            }
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

  component Line: Text {
    property bool small: false
    property bool bold: false
    width: parent ? parent.width : 0
    color: root.foreground
    font.family: root.fontFamily
    font.pixelSize: small ? Style.font.caption : Style.font.bodySmall
    font.bold: bold
    wrapMode: Text.WordWrap
    textFormat: Text.PlainText
  }

  component ActionRow: CursorSurface {
    id: action
    property string icon: ""
    property string label: ""
    property string hint: ""
    signal activated()
    width: parent ? parent.width : 0
    foreground: root.foreground
    hasCursor: actionMouse.containsMouse
    implicitHeight: actionRow.implicitHeight + Style.spacing.rowPaddingX
    MouseArea {
      id: actionMouse
      anchors.fill: parent
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      onClicked: action.activated()
    }
    RowLayout {
      id: actionRow
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.verticalCenter: parent.verticalCenter
      anchors.leftMargin: Style.space(10)
      anchors.rightMargin: Style.space(10)
      spacing: Style.space(8)
      Text { text: action.icon; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.icon }
      ColumnLayout {
        Layout.fillWidth: true
        spacing: 0
        Text { text: action.label; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.body }
        Text {
          Layout.fillWidth: true
          text: action.hint
          color: root.dim
          font.family: root.fontFamily
          font.pixelSize: Style.font.caption
          elide: Text.ElideRight
        }
      }
    }
  }
}
