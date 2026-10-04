import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

// Storage: every disk with its partitions and NVMe health, what uses the
// system disk (click to drill into folders), what is available, and what can
// be reclaimed. Data from storage.py (no root).
Panel {
  id: root
  moduleName: "juansebas7ian.storage"
  ipcTarget: "juansebas7ian.storage"
  manageIpc: false

  readonly property string backend: String(Qt.resolvedUrl("storage.py")).replace("file://", "")
  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property color urgent: bar ? bar.urgent : Color.urgent
  readonly property color dim: Qt.darker(foreground, 1.55)
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family

  property var ov: null
  property var bd: null
  property var cl: null
  property var steam: null
  property var fw: null
  property string selftestMsg: ""
  property var dirView: null
  property var dirStack: []
  readonly property real usedFrac: ov && ov.root.size > 0 ? ov.root.used / ov.root.size : 0
  readonly property bool lowSpace: !!ov && ov.root.free / ov.root.size < 0.1
  readonly property bool healthWarn: !!ov && ov.disks.some(function(d) {
    return d.health && ((d.health.criticalWarning || []).length > 0 || d.health.mediaErrors > 0
      || d.health.percentUsed >= 80 || d.health.spare <= d.health.spareThreshold)
  })

  function bytes(n) {
    n = Number(n || 0)
    if (n >= 1e12) return (n / 1e12).toFixed(2) + " TB"
    if (n >= 1e9) return (n / 1e9).toFixed(1) + " GB"
    if (n >= 1e6) return (n / 1e6).toFixed(0) + " MB"
    if (n >= 1e3) return (n / 1e3).toFixed(0) + " KB"
    return n + " B"
  }
  function run(proc, args) {
    if (proc.running) return
    proc.command = ["python3", backend].concat(args)
    proc.running = true
  }
  function openDir(path) {
    dirStack = dirView ? dirStack.concat([dirView.path]) : []
    run(dirProc, ["dir", path])
  }
  function back() {
    if (dirStack.length === 0) { dirView = null; return }
    var prev = dirStack[dirStack.length - 1]
    dirStack = dirStack.slice(0, -1)
    run(dirProc, ["dir", prev])
  }
  function inTerminal(cmd) {
    root.bar.run("omarchy-launch-floating-terminal-with-presentation " + root.bar.shellQuote(cmd))
    root.close()
  }

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  onOpenedChanged: if (opened) {
    run(ovProc, ["overview"])
    run(bdProc, ["breakdown"])
    run(clProc, ["cleanup"])
    run(steamProc, ["steam"])
    run(fwProc, ["firmware"])
    Qt.callLater(function() { keyCatcher.forceActiveFocus() })
  }

  Timer { interval: 600000; running: true; repeat: true; triggeredOnStart: true; onTriggered: root.run(ovProc, ["overview"]) }

  Process {
    id: ovProc
    stdout: StdioCollector { waitForEnd: true; onStreamFinished: { try { root.ov = JSON.parse(text) } catch (e) {} } }
  }
  Process {
    id: bdProc
    stdout: StdioCollector { waitForEnd: true; onStreamFinished: { try { root.bd = JSON.parse(text) } catch (e) {} } }
  }
  Process {
    id: clProc
    stdout: StdioCollector { waitForEnd: true; onStreamFinished: { try { root.cl = JSON.parse(text) } catch (e) {} } }
  }
  Process {
    id: steamProc
    stdout: StdioCollector { waitForEnd: true; onStreamFinished: { try { root.steam = JSON.parse(text) } catch (e) {} } }
  }
  Process {
    id: fwProc
    stdout: StdioCollector { waitForEnd: true; onStreamFinished: { try { root.fw = JSON.parse(text) } catch (e) {} } }
  }
  Process {
    id: testProc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        try {
          var r = JSON.parse(text)
          root.selftestMsg = r.ok ? "Short self-test started (about 2 min). Reopen the panel to see the result." : "Self-test: " + r.error
        } catch (e) {}
      }
    }
  }
  Process {
    id: dirProc
    stdout: StdioCollector { waitForEnd: true; onStreamFinished: { try { root.dirView = JSON.parse(text) } catch (e) {} } }
  }

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
    text: "󰋊"
    active: root.lowSpace || root.healthWarn
    tooltipText: !root.ov ? "Storage"
      : "System disk: " + root.bytes(root.ov.root.used) + " used · " + root.bytes(root.ov.root.free) + " free ("
        + Math.round(100 - root.usedFrac * 100) + "%)" + (root.healthWarn ? " · check disk health" : "")
    onPressed: function(code) {
      if (code === Qt.RightButton) root.inTerminal("dua interactive " + Quickshell.env("HOME"))
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
    contentWidth: panel.fittedContentWidth(Style.space(460))
    contentHeight: panel.fittedContentHeight(column.implicitHeight, Style.space(820))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      onCloseRequested: root.dirView ? root.back() : root.close()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onTextKey: function(t) { if (t === "r" || t === "R") { root.run(ovProc, ["overview"]); root.run(bdProc, ["breakdown"]); root.run(clProc, ["cleanup"]) } }

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
          spacing: Style.space(8)

          PanelHero {
            width: parent.width
            title: "Storage"
            meta: root.ov ? root.bytes(root.ov.root.free) + " free on the system disk" : "Reading disks…"
            foreground: root.foreground
            fontFamily: root.fontFamily
            iconComponent: Component {
              Text { text: "󰋊"; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.display }
            }
            trailingControl: Component {
              PanelActionButton {
                iconText: ""
                tooltipText: "Explore interactively (dua)"
                foreground: root.foreground
                fontFamily: root.fontFamily
                onClicked: root.inTerminal("dua interactive " + Quickshell.env("HOME"))
              }
            }
          }

          // ================================================= folder drill-down
          Column {
            visible: !!root.dirView
            width: parent.width
            spacing: Style.space(4)
            RowLayout {
              width: parent.width
              PanelActionButton {
                iconText: "󰁍"
                tooltipText: "Back (Esc)"
                foreground: root.foreground
                fontFamily: root.fontFamily
                onClicked: root.back()
              }
              Text {
                Layout.fillWidth: true
                text: root.dirView ? root.dirView.path + "  ·  " + root.bytes(root.dirView.total) : ""
                color: root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
                elide: Text.ElideLeft
              }
              PanelActionButton {
                iconText: "󰉋"
                tooltipText: "Open in the file manager"
                foreground: root.foreground
                fontFamily: root.fontFamily
                onClicked: { root.bar.run("xdg-open " + root.bar.shellQuote(root.dirView.path)); root.close() }
              }
            }
            Repeater {
              model: root.dirView ? root.dirView.items : []
              SizeRow {
                required property var modelData
                label: (modelData.isDir ? "󰉋 " : "󰈔 ") + modelData.name
                size: modelData.size
                total: root.dirView.total
                clickable: modelData.isDir
                onPicked: root.openDir(modelData.path)
              }
            }
          }

          // ================================================= disks
          Column {
            visible: !root.dirView
            width: parent.width
            spacing: Style.space(8)

            Repeater {
              model: root.ov ? root.ov.disks : []
              Column {
                id: disk
                required property var modelData
                width: column.width
                spacing: Style.space(3)
                readonly property var h: modelData.health
                readonly property var sys: modelData.partitions.filter(function(p) { return p.mounts.indexOf("/") >= 0 })[0]

                PanelSeparator { foreground: root.foreground }
                Pair {
                  label: "󰋊 " + disk.modelData.model + (disk.modelData.role ? " · " + disk.modelData.role : "")
                  value: root.bytes(disk.modelData.size)
                  strong: true
                }
                Meter {
                  visible: !!disk.sys
                  fraction: disk.sys ? disk.sys.used / disk.sys.fssize : 0
                  hot: root.lowSpace
                }
                Pair {
                  visible: !!disk.sys
                  label: "Used · free"
                  value: disk.sys ? root.bytes(disk.sys.used) + " · " + root.bytes(disk.sys.avail) : ""
                }
                Pair {
                  visible: !!disk.h
                  label: "Health"
                  value: disk.h ? (disk.h.percentUsed !== null ? disk.h.percentUsed + "% worn" : "") + " · spare " + disk.h.spare + "%"
                    + (disk.h.mediaErrors > 0 ? " · " + disk.h.mediaErrors + " media errors" : "") : ""
                  hot: !!disk.h && (disk.h.mediaErrors > 0 || disk.h.percentUsed >= 80 || (disk.h.criticalWarning || []).length > 0)
                }
                Pair {
                  visible: !!disk.h
                  label: "Written · read · on"
                  value: disk.h ? root.bytes(disk.h.written) + " · " + root.bytes(disk.h.read) + " · "
                    + Math.round(disk.h.powerOnHours / 24) + " days · " + disk.h.temp + "°C" : ""
                }
                Repeater {
                  model: disk.modelData.partitions
                  Pair {
                    required property var modelData
                    small: true
                    label: "    ".repeat(modelData.depth) + "└ " + modelData.name + "  " + (modelData.fstype || modelData.type)
                      + (modelData.label ? " \"" + modelData.label + "\"" : "")
                      + (modelData.mounts.length > 0 ? "  " + modelData.mounts.join(" ") : "")
                    value: modelData.used !== null && modelData.fssize
                      ? root.bytes(modelData.used) + " / " + root.bytes(modelData.fssize)
                      : root.bytes(modelData.size) + (modelData.fstype === "ntfs" ? " (not mounted)" : "")
                  }
                }
              }
            }

            // ================================================= what uses it
            PanelSeparator { foreground: root.foreground }
            PanelSectionHeader { text: "WHAT USES THE SYSTEM DISK"; foreground: root.foreground; fontFamily: root.fontFamily }
            Text {
              visible: !root.bd
              text: "Measuring folders…"
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
            }
            // Stacked bar: home · system · other · free
            Item {
              visible: !!root.bd && !!root.ov
              width: parent.width
              height: Style.space(10)
              readonly property real total: root.ov ? root.ov.root.size : 1
              Rectangle { anchors.fill: parent; radius: Style.space(3); color: Qt.alpha(root.foreground, 0.12) }
              Row {
                anchors.fill: parent
                Rectangle { height: parent.height; color: root.foreground; width: root.bd ? parent.width * Math.min(root.bd.home.total, root.ov.root.used) / parent.parent.total : 0 }
                Rectangle { height: parent.height; color: Qt.alpha(root.foreground, 0.55); width: root.bd ? parent.width * Math.max(0, root.ov.root.used - root.bd.home.total) / parent.parent.total : 0 }
              }
            }
            Pair {
              visible: !!root.bd
              label: "■ Your files (home)"
              value: root.bd ? root.bytes(root.bd.home.total) : ""
            }
            Pair {
              visible: !!root.bd
              label: "■ System, apps, swap" + (root.bd && root.bd.other > 0 ? ", snapshots" : "")
              value: root.bd ? root.bytes(root.bd.system.total + root.bd.other) : ""
            }
            Pair {
              visible: !!root.bd && root.bd.overMeasured > 0
              small: true
              label: "zstd compression saves about"
              value: root.bd ? root.bytes(root.bd.overMeasured) : ""
            }

            Text {
              visible: !!root.bd
              text: "HOME · click a folder to see inside"
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
            }
            Repeater {
              model: root.bd ? root.bd.home.items : []
              SizeRow {
                required property var modelData
                label: "󰉋 " + modelData.name
                size: modelData.size
                total: root.bd.home.total
                onPicked: root.openDir(modelData.path)
              }
            }
            Text {
              visible: !!root.bd
              text: "SYSTEM"
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
            }
            Repeater {
              model: root.bd ? root.bd.system.items : []
              SizeRow {
                required property var modelData
                label: modelData.name
                size: modelData.size
                total: root.bd.system.total
                onPicked: root.openDir(modelData.path)
              }
            }
            SizeRow {
              visible: !!root.bd && root.bd.other > 0
              label: "Snapshots, metadata, unreadable folders"
              size: root.bd ? root.bd.other : 0
              total: root.bd ? root.bd.system.total : 1
              clickable: false
            }

            // ================================================= steam
            PanelSeparator { visible: !!root.steam && root.steam.games.length + root.steam.tools.length > 0; foreground: root.foreground }
            PanelSectionHeader {
              visible: !!root.steam && root.steam.games.length + root.steam.tools.length > 0
              text: "STEAM · " + (root.steam ? root.bytes(root.steam.total) : "")
              foreground: root.foreground
              fontFamily: root.fontFamily
            }
            Repeater {
              model: root.steam ? root.steam.games : []
              Column {
                id: game
                required property var modelData
                width: column.width
                SizeRow {
                  label: "󰊗 " + game.modelData.name
                  size: game.modelData.total
                  total: root.steam.total
                  onPicked: root.openDir(game.modelData.path)
                }
                Text {
                  width: parent.width
                  leftPadding: Style.space(8)
                  text: "game " + root.bytes(game.modelData.size)
                    + (game.modelData.prefix > 0 ? " · Proton prefix " + root.bytes(game.modelData.prefix) : "")
                    + (game.modelData.shaders > 0 ? " · shaders " + root.bytes(game.modelData.shaders) : "")
                    + (game.modelData.lastPlayed > 0 ? " · played " + new Date(game.modelData.lastPlayed * 1000).toLocaleDateString() : " · never played")
                  color: root.dim
                  font.family: root.fontFamily
                  font.pixelSize: Style.font.caption
                  elide: Text.ElideRight
                }
              }
            }
            Repeater {
              model: root.steam ? root.steam.tools : []
              SizeRow {
                required property var modelData
                label: "󰏗 " + modelData.name
                size: modelData.total
                total: root.steam.total
                onPicked: root.openDir(modelData.path)
              }
            }
            SizeRow {
              visible: !!root.steam && root.steam.client > 0
              label: "Steam client, downloads, logs"
              size: root.steam ? root.steam.client : 0
              total: root.steam ? root.steam.total : 1
              onPicked: root.openDir(root.steam.path)
            }
            Text {
              visible: !!root.steam && root.steam.games.length > 0
              width: parent.width
              text: "Uninstall a game from Steam (Library → Manage → Uninstall) to free its space and its Proton prefix."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
              wrapMode: Text.WordWrap
            }

            // ================================================= health & firmware (Magician)
            PanelSeparator { visible: !!root.fw; foreground: root.foreground }
            PanelSectionHeader { visible: !!root.fw; text: "DISK HEALTH & FIRMWARE"; foreground: root.foreground; fontFamily: root.fontFamily }
            Repeater {
              model: root.fw ? root.fw.drives : []
              Column {
                id: drv
                required property var modelData
                width: column.width
                spacing: Style.space(3)
                readonly property real tbwUsed: drv.modelData.tbw && drv.modelData.written ? drv.modelData.written / (drv.modelData.tbw * 1e12) : -1
                Pair { label: (drv.modelData.vendor === "Samsung" ? "Samsung Magician · " : "") + drv.modelData.model; value: ""; strong: true }
                Pair {
                  label: "Firmware"
                  value: drv.modelData.firmware + (drv.modelData.upToDate ? " · up to date ✓" : " → " + drv.modelData.latest + " available")
                  hot: !drv.modelData.upToDate
                }
                Pair { small: true; label: "  checked against"; value: drv.modelData.source }
                Pair {
                  visible: drv.tbwUsed >= 0
                  label: "Endurance (TBW)"
                  value: root.bytes(drv.modelData.written) + " of " + drv.modelData.tbw + " TB rated · " + Math.round(drv.tbwUsed * 100) + "%"
                  hot: drv.tbwUsed >= 0.8
                }
                Meter { visible: drv.tbwUsed >= 0; fraction: drv.tbwUsed; hot: drv.tbwUsed >= 0.8 }
                Pair {
                  visible: !!drv.modelData.health && drv.modelData.health.unsafeShutdowns !== undefined
                  small: true
                  label: "  unsafe shutdowns · media errors"
                  value: drv.modelData.health ? drv.modelData.health.unsafeShutdowns + " · " + drv.modelData.health.mediaErrors : ""
                }
                Pair {
                  visible: !!drv.modelData.selftest && drv.modelData.selftest.status !== ""
                  small: true
                  label: "  last self-test"
                  value: drv.modelData.selftest ? drv.modelData.selftest.status + (drv.modelData.selftest.remaining > 0 ? " · " + drv.modelData.selftest.remaining + "% left" : "") : ""
                }
                Row {
                  spacing: Style.space(6)
                  PanelActionButton {
                    visible: drv.modelData.udisks !== ""
                    iconText: "󰙨"
                    tooltipText: "Run a short SMART self-test (asks for your password)"
                    foreground: root.foreground
                    fontFamily: root.fontFamily
                    onClicked: {
                      if (testProc.running) return
                      testProc.command = ["python3", root.backend, "selftest", drv.modelData.udisks, "short"]
                      testProc.running = true
                    }
                  }
                  PanelActionButton {
                    visible: drv.modelData.download !== ""
                    iconText: "󰇚"
                    tooltipText: drv.modelData.vendor === "Samsung" ? "Samsung firmware ISO (boot it from USB to update)" : "Update with fwupd"
                    foreground: root.foreground
                    fontFamily: root.fontFamily
                    onClicked: drv.modelData.download.indexOf("http") === 0
                      ? (root.bar.run("xdg-open " + root.bar.shellQuote(drv.modelData.download)), root.close())
                      : root.inTerminal(drv.modelData.download)
                  }
                }
                Text {
                  visible: !!drv.modelData.note
                  width: parent.width
                  text: drv.modelData.note || ""
                  color: root.dim
                  font.family: root.fontFamily
                  font.pixelSize: Style.font.caption
                  wrapMode: Text.WordWrap
                }
              }
            }
            Text {
              visible: root.selftestMsg !== ""
              width: parent.width
              text: root.selftestMsg
              color: root.foreground
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
              wrapMode: Text.WordWrap
            }

            // ================================================= available
            PanelSeparator { foreground: root.foreground }
            PanelSectionHeader { text: "AVAILABLE"; foreground: root.foreground; fontFamily: root.fontFamily }
            Pair {
              visible: !!root.ov
              label: "System disk (btrfs)"
              value: root.ov ? root.bytes(root.ov.root.free) + " free" : ""
            }
            Pair {
              visible: !!root.ov && !!root.ov.btrfs.freeMin
              small: true
              label: "  never yet allocated · worst case"
              value: root.ov && root.ov.btrfs.unallocated ? root.bytes(root.ov.btrfs.unallocated) + " · " + root.bytes(root.ov.btrfs.freeMin) : ""
            }
            Pair {
              visible: !!root.ov
              label: "/boot (EFI)"
              value: {
                if (!root.ov) return ""
                var b = root.ov.disks.length > 0 ? root.ov.disks[0].partitions.filter(function(p) { return p.mounts.indexOf("/boot") >= 0 })[0] : null
                return b ? root.bytes(b.avail) + " free of " + root.bytes(b.fssize) : ""
              }
            }
            Repeater {
              model: root.ov ? root.ov.extra : []
              Pair {
                required property var modelData
                label: modelData.name
                value: root.bytes(modelData.free) + " free of " + root.bytes(modelData.size)
              }
            }
            Repeater {
              model: root.ov ? root.ov.swaps : []
              Pair {
                required property var modelData
                small: true
                label: "Swap " + modelData.name
                value: root.bytes(modelData.used) + " / " + root.bytes(modelData.size)
              }
            }

            // ================================================= reclaim
            PanelSeparator { visible: !!root.cl; foreground: root.foreground }
            PanelSectionHeader { visible: !!root.cl; text: "CAN BE FREED"; foreground: root.foreground; fontFamily: root.fontFamily }
            Repeater {
              model: root.cl ? root.cl.items : []
              CursorSurface {
                id: rc
                required property var modelData
                width: column.width
                foreground: root.foreground
                hasCursor: rcMouse.containsMouse
                implicitHeight: rcCol.implicitHeight + Style.space(8)
                MouseArea {
                  id: rcMouse
                  anchors.fill: parent
                  hoverEnabled: true
                  cursorShape: Qt.PointingHandCursor
                  onClicked: rc.modelData.command ? root.inTerminal(rc.modelData.command)
                    : root.openDir(rc.modelData.open)
                }
                Column {
                  id: rcCol
                  anchors.left: parent.left
                  anchors.right: parent.right
                  anchors.verticalCenter: parent.verticalCenter
                  anchors.leftMargin: Style.space(6)
                  anchors.rightMargin: Style.space(6)
                  Pair { label: rc.modelData.name; value: root.bytes(rc.modelData.size) }
                  Text {
                    width: parent.width
                    text: (rc.modelData.detail ? rc.modelData.detail + " · " : "")
                      + (rc.modelData.command ? "click: " + rc.modelData.command : "click to review")
                    color: root.dim
                    font.family: root.fontFamily
                    font.pixelSize: Style.font.caption
                    elide: Text.ElideRight
                  }
                }
              }
            }
          }
        }
      }
    }
  }

  // ---------------------------------------------------------------- parts

  component Meter: Item {
    property real fraction: 0
    property bool hot: false
    width: parent ? parent.width : 0
    height: Style.space(6)
    Rectangle { anchors.fill: parent; radius: height / 2; color: Qt.alpha(root.foreground, 0.15) }
    Rectangle {
      height: parent.height
      radius: height / 2
      color: parent.hot ? root.urgent : root.foreground
      width: parent.width * Math.max(0, Math.min(1, parent.fraction))
    }
  }

  component SizeRow: CursorSurface {
    id: sr
    property string label: ""
    property real size: 0
    property real total: 1
    property bool clickable: true
    signal picked()
    width: parent ? parent.width : 0
    foreground: root.foreground
    hasCursor: srMouse.containsMouse && clickable
    implicitHeight: Style.space(26)
    // Bar behind the row, scaled to the group's total
    Rectangle {
      anchors.left: parent.left
      anchors.top: parent.top
      anchors.bottom: parent.bottom
      radius: Style.space(3)
      color: Qt.alpha(root.foreground, 0.1)
      width: parent.width * Math.max(0, Math.min(1, sr.size / Math.max(1, sr.total)))
    }
    MouseArea {
      id: srMouse
      anchors.fill: parent
      hoverEnabled: true
      cursorShape: sr.clickable ? Qt.PointingHandCursor : Qt.ArrowCursor
      onClicked: if (sr.clickable) sr.picked()
    }
    Text {
      anchors.left: parent.left
      anchors.leftMargin: Style.space(6)
      anchors.right: srSize.left
      anchors.rightMargin: Style.space(8)
      anchors.verticalCenter: parent.verticalCenter
      text: sr.label
      color: root.foreground
      font.family: root.fontFamily
      font.pixelSize: Style.font.bodySmall
      elide: Text.ElideMiddle
    }
    Text {
      id: srSize
      anchors.right: parent.right
      anchors.rightMargin: Style.space(6)
      anchors.verticalCenter: parent.verticalCenter
      text: root.bytes(sr.size)
      color: root.foreground
      font.family: root.fontFamily
      font.pixelSize: Style.font.bodySmall
    }
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
      color: root.foreground
      opacity: parent.strong ? 1.0 : 0.6
      font.family: root.fontFamily
      font.pixelSize: parent.small ? Style.font.caption : Style.font.bodySmall
      font.bold: parent.strong
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
