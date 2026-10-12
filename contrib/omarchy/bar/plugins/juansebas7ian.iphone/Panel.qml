import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

// iPhone in the bar: status (battery, storage, trust), photo import, app files, local backups,
// USB tethering, screen mirroring (UxPlay/AirPlay) and KDE Connect. Everything goes through the Go
// backend omarchy-iphone (libimobiledevice + ifuse; no root).
Panel {
  id: root
  moduleName: "juansebas7ian.iphone"
  ipcTarget: "juansebas7ian.iphone"
  manageIpc: false

  readonly property string ctl: Quickshell.env("HOME") + "/.local/bin/omarchy-iphone"
  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property color urgent: bar ? bar.urgent : Color.urgent
  readonly property color dim: Qt.darker(foreground, 1.55)
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family

  property var info: null
  readonly property var devices: info ? (info.devices || []) : []
  readonly property var phone: devices.length > 0 ? devices[0] : null
  readonly property bool trusted: !!phone && phone.trusted
  readonly property var photos: info ? info.photos : null
  readonly property var backup: info ? info.backup : null
  readonly property var cfg: info ? info.config : null
  readonly property var kde: info ? info.kdeconnect : null
  readonly property var kdeDevices: kde ? (kde.devices || []) : []
  readonly property var scan: phone && photos && photos.scans ? photos.scans[phone.udid] : null

  property string tab: "overview"
  property string message: ""
  property bool messageIsError: false
  property var apps: []
  property bool appsLoading: false
  property string jobName: ""          // "import" | "backup" while running from this panel
  property string jobStatus: ""
  property real jobFraction: -1
  readonly property bool jobRunning: jobProcess.running || (photos && photos.running) || (backup && backup.running)

  function fmtBytes(n) {
    if (!n || n <= 0) return "0 B"
    var u = ["B", "KB", "MB", "GB", "TB"], i = 0
    while (n >= 1000 && i < u.length - 1) { n /= 1000; i++ }
    return (i >= 3 ? n.toFixed(1) : Math.round(n)) + " " + u[i]
  }
  function ago(iso) {
    if (!iso) return "never"
    var s = (Date.now() - new Date(iso).getTime()) / 1000
    if (s < 90) return "just now"
    if (s < 3600) return Math.round(s / 60) + " min ago"
    if (s < 86400 * 2) return Math.round(s / 3600) + " h ago"
    return Math.round(s / 86400) + " days ago"
  }
  function refresh() { if (!statusProcess.running) statusProcess.running = true }
  function say(text, isError) { message = text; messageIsError = !!isError }
  onMessageChanged: if (message !== "") messageTimer.restart()
  Timer { id: messageTimer; interval: root.messageIsError ? 20000 : 8000; onTriggered: root.message = "" }

  function act(args, okText) {
    if (actionProcess.running) return
    message = ""
    actionProcess.okText = okText || ""
    actionProcess.command = [ctl].concat(args)
    actionProcess.running = true
  }
  function startJob(kind) {
    if (jobProcess.running) return
    jobName = kind; jobStatus = "starting…"; jobFraction = -1; message = ""
    jobProcess.command = [ctl, kind]
    jobProcess.running = true
  }
  function terminal(cmd) { if (bar) bar.run("uwsm-app -- xdg-terminal-exec bash -c " + bar.shellQuote(cmd + "; read -p 'Enter to close'")); root.close() }
  function openPath(p) { if (bar) bar.run("xdg-open " + bar.shellQuote(p)); root.close() }
  function loadApps() { if (trusted && !appsProcess.running) { appsLoading = true; appsProcess.running = true } }

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight
  onOpenedChanged: if (opened) { refresh(); Qt.callLater(function() { keyCatcher.forceActiveFocus() }) }
  onTabChanged: if (tab === "files" && apps.length === 0) loadApps()

  Timer {
    interval: root.opened || root.jobRunning ? 3000 : 20000
    running: true
    repeat: true
    triggeredOnStart: true
    onTriggered: root.refresh()
  }
  Process {
    id: statusProcess
    command: [root.ctl, "status"]
    stdout: StdioCollector { waitForEnd: true; onStreamFinished: { try { root.info = JSON.parse(text) } catch (e) {} } }
  }
  Process {
    id: appsProcess
    command: [root.ctl, "apps"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        root.appsLoading = false
        try { var r = JSON.parse(text); root.apps = r.apps || []; if (r.ok === false) root.say(r.error, true) } catch (e) {}
      }
    }
  }
  Process {
    id: jobProcess
    stdout: SplitParser {
      onRead: function(line) {
        var d
        try { d = JSON.parse(line) } catch (e) { return }
        if (d.progress) {
          if (root.jobName === "import") {
            root.jobFraction = d.total > 0 ? d.done / d.total : -1
            root.jobStatus = d.total === 0 ? "nothing new" : d.done + " of " + d.total + (d.file ? " · " + d.file : "")
          } else {
            if (d.pct >= 0) root.jobFraction = d.pct / 100
            root.jobStatus = d.line || ""
          }
        } else if (d.ok === false) {
          root.say((root.jobName === "import" ? "Import failed: " : "Backup failed: ") + d.error, true)
        } else if (d.ok) {
          if (root.jobName === "import" && d.result) {
            var r = d.result
            root.say(r.imported + " new photos and videos" + (r.already ? " (" + r.already + " were already there)" : "")
              + (r.failed ? " · " + r.failed + " failed" : ""), r.failed > 0)
          } else root.say("Backup finished", false)
        }
      }
    }
    onExited: { root.jobName = ""; root.jobFraction = -1; root.refresh() }
  }
  Process {
    id: actionProcess
    property string okText: ""
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        try {
          var r = JSON.parse(text.trim().split("\n").pop())
          if (r.ok === false || r.error) root.say(r.error || "Action failed", true)
          else if (actionProcess.okText) root.say(actionProcess.okText, false)
        } catch (e) {}
        root.refresh()
      }
    }
  }

  IpcHandler {
    target: root.ipcTarget
    function open(): void { root.open() }
    function close(): void { root.close() }
    function toggle(): void { root.toggle() }
    function refresh(): string { root.refresh(); return "ok" }
    function tab(name: string): void { root.tab = name; root.open() }
  }

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    // grows with the battery text, see the BarIconButton trap in omarchy.md
    fixedWidth: vertical ? -1 : Math.max(slotSize, Math.ceil(glyphPaintedWidth) + Style.space(10))
    text: root.jobRunning ? "󰄜 " + (root.jobFraction >= 0 ? Math.round(root.jobFraction * 100) + "%" : "…")
      : root.trusted && root.phone.battery ? "󰄜 " + root.phone.battery.level + "%" : "󰄜"
    dimmed: !root.phone
    tooltipText: !root.phone ? "iPhone: not connected" : !root.trusted ? "iPhone: tap Trust on the phone"
      : (root.phone.name || "iPhone") + (root.phone.battery ? " · " + root.phone.battery.level + "%" + (root.phone.battery.charging ? " charging" : "") : "")
    onPressed: function(code) {
      if (code === Qt.RightButton && root.trusted) root.startJob("import")
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
    contentHeight: panel.fittedContentHeight(column.implicitHeight, Style.space(680))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      onCloseRequested: root.close()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onTextKey: function(t) {
        var tabs = ["overview", "photos", "files", "backup", "mirror", "connect"]
        if (t === "r" || t === "R") root.refresh()
        else if (t >= "1" && t <= "6") root.tab = tabs[Number(t) - 1]
      }

      Flickable {
        id: panelFlick
        anchors.fill: parent
        contentWidth: width
        contentHeight: column.implicitHeight
        clip: true
        boundsBehavior: Flickable.StopAtBounds
        interactive: contentHeight > height
        ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

        Column {
          id: column
          width: panelFlick.width - Style.space(12)   // margin always (a scrollbar-dependent width loops the layout)
          spacing: Style.space(10)

          PanelHero {
            width: parent.width
            title: root.phone && root.phone.name ? root.phone.name : "iPhone"
            meta: !root.info ? "Loading…" : !root.phone ? "Not connected · plug it in by cable"
              : !root.trusted ? (root.phone.error || "Not paired")
              : [root.phone.model, root.phone.ios ? "iOS " + root.phone.ios : "", root.phone.conn === "network" ? "Wi-Fi" : "USB"].filter(function(s) { return s }).join(" · ")
            foreground: root.foreground
            fontFamily: root.fontFamily
            iconComponent: Component { Text { text: "󰄜"; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.display } }
            trailingControl: Component {
              PanelActionButton { iconText: "󰑐"; tooltipText: "Refresh (r)"; foreground: root.foreground; fontFamily: root.fontFamily; onClicked: root.refresh() }
            }
          }

          Flow {
            width: parent.width
            spacing: Style.space(4)
            Chip { label: "Overview"; on: root.tab === "overview"; onClicked: root.tab = "overview" }
            Chip { label: "Photos"; on: root.tab === "photos"; onClicked: root.tab = "photos" }
            Chip { label: "Files"; on: root.tab === "files"; onClicked: root.tab = "files" }
            Chip { label: "Backup"; on: root.tab === "backup"; onClicked: root.tab = "backup" }
            Chip { label: "Mirror"; on: root.tab === "mirror"; onClicked: root.tab = "mirror" }
            Chip { label: "Connect"; on: root.tab === "connect"; onClicked: root.tab = "connect" }
          }

          Text {
            visible: root.message !== ""
            width: parent.width
            text: root.message
            color: root.messageIsError ? root.urgent : Color.accent
            font.family: root.fontFamily
            font.pixelSize: Style.font.bodySmall
            wrapMode: Text.WordWrap
          }

          Column {
            visible: root.jobRunning
            width: parent.width
            spacing: Style.space(4)
            Text {
              width: parent.width
              text: (root.jobName === "backup" || (root.backup && root.backup.running) ? "Backing up" : "Importing photos")
                + (root.jobStatus ? " · " + root.jobStatus : "")
              color: root.foreground
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
              elide: Text.ElideRight
            }
            Meter { width: parent.width; fraction: Math.max(0, root.jobFraction) }
          }

          // ============================================== Overview
          Column {
            visible: root.tab === "overview"
            width: parent.width
            spacing: Style.space(8)

            Text {
              visible: !root.phone
              width: parent.width
              text: "Connect the iPhone with its cable and unlock it. The first time, tap Trust on the phone and type its passcode.\n\nWireless: Mirror (AirPlay) and Connect (KDE Connect) work over the home Wi-Fi without the cable."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.body
              wrapMode: Text.WordWrap
            }
            RowLayout {
              visible: !!root.phone && !root.trusted
              width: parent.width
              Text {
                Layout.fillWidth: true
                text: root.phone ? (root.phone.error || "Not paired") : ""
                color: root.urgent
                font.family: root.fontFamily
                font.pixelSize: Style.font.body
                wrapMode: Text.WordWrap
              }
              PanelActionButton { iconText: "󰌹"; tooltipText: "Pair (then tap Trust on the iPhone)"; foreground: root.foreground; fontFamily: root.fontFamily
                onClicked: root.act(["pair", root.phone.udid], "Paired: the iPhone trusts this PC") }
            }
            Column {
              visible: root.trusted && !!root.phone.battery
              width: parent.width
              spacing: Style.space(4)
              InfoPair { label: "Battery"; value: root.trusted && root.phone.battery ? root.phone.battery.level + "%" + (root.phone.battery.full ? " · full" : root.phone.battery.charging ? " · charging" : "") : "" }
              Meter { width: parent.width; fraction: root.trusted && root.phone.battery ? root.phone.battery.level / 100 : 0; invert: true }
            }
            Column {
              visible: root.trusted && !!root.phone.storage
              width: parent.width
              spacing: Style.space(4)
              InfoPair { label: "Storage"; value: root.trusted && root.phone.storage ? root.fmtBytes(root.phone.storage.total - root.phone.storage.free) + " used of " + root.fmtBytes(root.phone.storage.total) : "" }
              Meter { width: parent.width; fraction: root.trusted && root.phone.storage ? 1 - root.phone.storage.free / root.phone.storage.total : 0 }
            }
            InfoPair { visible: root.trusted; label: "New photos"; value: root.scan ? root.scan.pending + " (" + root.fmtBytes(root.scan.pendingBytes) + ")" : "—" }
            InfoPair { visible: root.trusted; label: "Last backup"; value: root.backup && root.backup.last ? root.ago(root.backup.last.time) + (root.backup.last.ok ? "" : " · failed") : "never" }
            InfoPair {
              label: "Internet over USB"
              value: root.info && root.info.tether.iface ? (root.info.tether.connected ? "connected (" + root.info.tether.iface + ")" : root.info.tether.state || root.info.tether.iface)
                : "off · turn on Personal Hotspot on the iPhone"
            }
            InfoPair { label: "Screen mirroring"; value: root.info && root.info.airplay.running ? "on (\"" + root.info.airplay.name + "\")" : "off" }
            InfoPair { label: "KDE Connect"; value: root.kdeDevices.filter(function(d) { return d.reachable && d.paired }).length > 0 ? "connected" : root.kde && root.kde.installed ? "not connected" : "not installed" }
            RowLayout {
              visible: root.info && root.info.mounts && root.info.mounts.length > 0
              width: parent.width
              Text { Layout.fillWidth: true; text: root.info ? root.info.mounts.length + " iPhone folder(s) open" : ""; color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.caption }
              PanelActionButton { iconText: "󰕓"; tooltipText: "Release the iPhone folders (eject)"; foreground: root.foreground; fontFamily: root.fontFamily; onClicked: root.act(["eject"], "iPhone folders released") }
            }
          }

          // ============================================== Photos
          Column {
            visible: root.tab === "photos"
            width: parent.width
            spacing: Style.space(8)
            Text {
              width: parent.width
              text: "Photos and videos are copied as they are (HEIC, MOV) to " + (root.cfg ? root.cfg.photosDir : "~/Pictures/iPhone") + "/<year>/. Nothing is deleted from the iPhone; each photo is copied once."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
              wrapMode: Text.WordWrap
            }
            InfoPair { label: "New on the iPhone"; value: !root.trusted ? "—" : root.scan ? root.scan.pending + " of " + root.scan.total + " · " + root.fmtBytes(root.scan.pendingBytes) + " · checked " + root.ago(root.scan.time) : "not checked yet" }
            InfoPair {
              label: "Last import"
              value: root.photos && root.photos.last ? root.ago(root.photos.last.time) + " · " + root.photos.last.imported + " new" + (root.photos.last.failed ? " · " + root.photos.last.failed + " failed" : "") : "never"
            }
            Flow {
              width: parent.width
              spacing: Style.space(6)
              Chip { label: "Import now"; on: false; visible: root.trusted && !root.jobRunning; onClicked: root.startJob("import") }
              Chip { label: "Check for new"; visible: root.trusted && !root.jobRunning; onClicked: root.act(["scan"], "Checked") }
              Chip { label: "Open imported"; onClicked: root.openPath(root.cfg ? root.cfg.photosDir : "") }
              Chip { label: "Browse iPhone"; visible: root.trusted; onClicked: root.act(["open", "photos"], "") }
            }
            PanelSeparator { foreground: root.foreground }
            RowLayout {
              width: parent.width
              Text { Layout.fillWidth: true; text: "Import automatically when plugged in"; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.body; wrapMode: Text.WordWrap }
              Chip { label: root.cfg && root.cfg.autoImport ? "On" : "Off"; on: !!root.cfg && root.cfg.autoImport
                onClicked: root.act(["set", "autoImport", root.cfg && root.cfg.autoImport ? "false" : "true"], "") }
            }
            Text {
              width: parent.width
              text: "Right-click the bar icon imports too."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
            }
          }

          // ============================================== Files
          Column {
            visible: root.tab === "files"
            width: parent.width
            spacing: Style.space(6)
            Text {
              width: parent.width
              text: !root.trusted ? "Connect and trust the iPhone to see its files."
                : "Apps that share files (Files › On My iPhone). Opening one shows it in the file manager: copy in or out, then eject."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
              wrapMode: Text.WordWrap
            }
            ModelRow {
              visible: root.trusted
              width: parent.width
              title: "Camera roll (DCIM)"
              meta: "every photo and video, read-only browsing"
              actions: [{ icon: "󰉋", tip: "Open in the file manager", run: function() { root.act(["open", "photos"], "") } }]
            }
            Text { visible: root.appsLoading; text: "Loading apps…"; color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.bodySmall }
            Repeater {
              model: root.trusted ? root.apps : []
              ModelRow {
                required property var modelData
                width: parent.width
                title: modelData.name
                meta: modelData.id + " · " + modelData.version
                actions: [{ icon: "󰉋", tip: "Open its files", run: function() { root.act(["open", "app", modelData.id], "") } }]
              }
            }
            Flow {
              visible: root.trusted
              width: parent.width
              spacing: Style.space(6)
              Chip { label: "Reload apps"; onClicked: root.loadApps() }
              Chip { label: "Eject"; onClicked: root.act(["eject"], "iPhone folders released") }
            }
          }

          // ============================================== Backup
          Column {
            visible: root.tab === "backup"
            width: parent.width
            spacing: Style.space(8)
            Text {
              width: parent.width
              text: "A full local backup like Finder/iTunes makes, in " + (root.backup ? root.backup.dir : "~/iPhoneBackup") + " (its own btrfs subvolume; not uploaded to Drive). The first one takes long; later ones only copy changes."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
              wrapMode: Text.WordWrap
            }
            InfoPair {
              label: "Last backup"
              value: root.backup && root.backup.last ? root.ago(root.backup.last.time) + (root.backup.last.bytes ? " · " + root.fmtBytes(root.backup.last.bytes) : "")
                + (root.backup.last.ok ? "" : " · FAILED: " + (root.backup.last.error || "")) : "never"
            }
            InfoPair {
              visible: root.trusted
              label: "Encryption"
              value: root.trusted && root.phone.backupEncrypted !== undefined ? (root.phone.backupEncrypted ? "on (passwords and Health included)" : "off (passwords and Health are left out)") : "—"
            }
            Flow {
              width: parent.width
              spacing: Style.space(6)
              Chip { label: "Back up now"; visible: root.trusted && !root.jobRunning; onClicked: root.startJob("backup") }
              Chip { label: root.trusted && root.phone.backupEncrypted ? "Turn encryption off" : "Turn encryption on"; visible: root.trusted
                onClicked: root.terminal(root.ctl + " encryption " + (root.phone.backupEncrypted ? "off" : "on")) }
              Chip { label: "Open folder"; onClicked: root.openPath(root.backup ? root.backup.dir : "") }
            }
            PanelSeparator { foreground: root.foreground }
            RowLayout {
              width: parent.width
              Text { Layout.fillWidth: true; text: "Back up automatically when plugged in"; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.body; wrapMode: Text.WordWrap }
              Chip { label: "Off"; on: !!root.cfg && root.cfg.autoBackupDays === 0; onClicked: root.act(["set", "autoBackupDays", "0"], "") }
              Chip { label: "Weekly"; on: !!root.cfg && root.cfg.autoBackupDays === 7; onClicked: root.act(["set", "autoBackupDays", "7"], "") }
              Chip { label: "Monthly"; on: !!root.cfg && root.cfg.autoBackupDays === 30; onClicked: root.act(["set", "autoBackupDays", "30"], "") }
            }
          }

          // ============================================== Mirror (AirPlay)
          Column {
            visible: root.tab === "mirror"
            width: parent.width
            spacing: Style.space(8)
            RowLayout {
              width: parent.width
              Text {
                Layout.fillWidth: true
                text: !root.info ? "" : !root.info.airplay.installed ? "UxPlay is not installed (contrib/omarchy/iphone/setup-system.sh)"
                  : root.info.airplay.running ? "Receiver on: \"" + root.info.airplay.name + "\"" : "Receiver off"
                color: root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.body
                wrapMode: Text.WordWrap
              }
              Chip {
                visible: !!root.info && root.info.airplay.installed
                label: root.info && root.info.airplay.running ? "Turn off" : "Turn on"
                on: !!root.info && root.info.airplay.running
                onClicked: root.act(["airplay", root.info.airplay.running ? "off" : "on"], root.info.airplay.running ? "Receiver off" : "Receiver on: mirror from the iPhone now")
              }
            }
            Text {
              width: parent.width
              text: "On the iPhone (same Wi-Fi): Control Center › Screen Mirroring › " + (root.info ? root.info.airplay.name : "Omarchy")
                + ". A window opens here with the iPhone screen and sound; stop from the iPhone. Videos and music apps can also AirPlay to it."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
              wrapMode: Text.WordWrap
            }
          }

          // ============================================== Connect (KDE Connect)
          Column {
            visible: root.tab === "connect"
            width: parent.width
            spacing: Style.space(6)
            Text {
              width: parent.width
              text: !root.kde || !root.kde.installed ? "KDE Connect is not installed (contrib/omarchy/iphone/setup-system.sh)."
                : "Install KDE Connect from the App Store on the iPhone, open it on the same Wi-Fi and pair. Then: shared clipboard, send files and links from the iPhone's Share menu, ring the phone."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
              wrapMode: Text.WordWrap
            }
            Text {
              visible: !!root.kde && root.kde.installed && root.kdeDevices.length === 0
              width: parent.width
              text: "No devices seen yet."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
            }
            Repeater {
              model: root.kdeDevices
              ModelRow {
                required property var modelData
                width: parent.width
                title: modelData.name
                meta: (modelData.paired ? "paired" : "not paired") + " · " + (modelData.reachable ? "reachable" : "not reachable")
                actions: modelData.paired
                  ? (modelData.reachable ? [
                      { icon: "󰅍", tip: "Send this PC's clipboard", run: function() { root.act(["kde", "clipboard", modelData.id], "Clipboard sent") } },
                      { icon: "󰂞", tip: "Ring the phone (find it)", run: function() { root.act(["kde", "ring", modelData.id], "Ringing") } },
                      { icon: "󰍡", tip: "Ping", run: function() { root.act(["kde", "ping", modelData.id], "Ping sent") } }] : [])
                    .concat([{ icon: "󰌸", tip: "Unpair", run: function() { root.act(["kde", "unpair", modelData.id], "Unpaired") } }])
                  : [{ icon: "󰌹", tip: "Pair (accept on the iPhone)", run: function() { root.act(["kde", "pair", modelData.id], "Pairing requested: accept it on the iPhone") } }]
              }
            }
          }
        }
      }
    }
  }

  component Meter: Item {
    property real fraction: 0
    property bool invert: false   // battery: low is bad
    implicitHeight: Style.space(6)
    Rectangle { anchors.fill: parent; radius: height / 2; color: Qt.alpha(root.foreground, 0.15) }
    Rectangle {
      width: parent.width * Math.max(0, Math.min(1, parent.fraction))
      height: parent.height
      radius: height / 2
      color: (parent.invert ? parent.fraction < 0.15 : parent.fraction > 0.9) ? root.urgent : root.foreground
    }
  }

  component Chip: Rectangle {
    id: chip
    property string label: ""
    property bool on: false
    signal clicked()
    radius: Style.space(4)
    color: on ? Qt.alpha(root.foreground, 0.28) : chipMouse.containsMouse ? Qt.alpha(root.foreground, 0.18) : Qt.alpha(root.foreground, 0.08)
    implicitWidth: chipText.implicitWidth + Style.space(14)
    implicitHeight: chipText.implicitHeight + Style.space(6)
    Text { id: chipText; anchors.centerIn: parent; text: chip.label; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.caption }
    MouseArea { id: chipMouse; anchors.fill: parent; hoverEnabled: true; cursorShape: Qt.PointingHandCursor; onClicked: chip.clicked() }
  }

  component ModelRow: CursorSurface {
    id: row
    property string title: ""
    property string meta: ""
    property var actions: []
    foreground: root.foreground
    hasCursor: hover.containsMouse
    implicitHeight: rowLayout.implicitHeight + Style.spacing.rowPaddingX
    MouseArea { id: hover; anchors.fill: parent; hoverEnabled: true; acceptedButtons: Qt.NoButton }
    RowLayout {
      id: rowLayout
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.verticalCenter: parent.verticalCenter
      anchors.leftMargin: Style.space(10)
      anchors.rightMargin: Style.space(6)
      spacing: Style.space(6)
      ColumnLayout {
        Layout.fillWidth: true
        spacing: Style.space(1)
        Text { Layout.fillWidth: true; text: row.title; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.body; elide: Text.ElideRight }
        Text { Layout.fillWidth: true; text: row.meta; color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.caption; elide: Text.ElideRight }
      }
      Repeater {
        model: row.actions
        PanelActionButton {
          required property var modelData
          iconText: modelData.icon
          tooltipText: modelData.tip
          foreground: root.foreground
          fontFamily: root.fontFamily
          onClicked: modelData.run()
        }
      }
    }
  }

  component InfoPair: Row {
    property string label: ""
    property string value: ""
    width: parent.width
    spacing: Style.space(8)
    Text { text: label; color: root.foreground; opacity: 0.6; font.family: root.fontFamily; font.pixelSize: Style.font.bodySmall }
    Item { width: Math.max(0, parent.width - parent.children[0].implicitWidth - parent.children[2].width - parent.spacing * 2); height: 1 }
    Text {
      width: Math.min(implicitWidth, parent.width - parent.children[0].implicitWidth - parent.spacing * 2)
      text: value
      color: root.foreground
      font.family: root.fontFamily
      font.pixelSize: Style.font.bodySmall
      elide: Text.ElideMiddle
    }
  }
}
