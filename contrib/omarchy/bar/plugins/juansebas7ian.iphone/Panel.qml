import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

// iPhone / iPad in the bar: status (battery, storage, trust), photo import (+ Google Photos), app files, local
// backups (+ Google Drive), freeing space safely, USB tethering, screen mirroring (UxPlay/AirPlay) and KDE Connect.
// Everything goes through the Go backend omarchy-iphone (libimobiledevice + ifuse; no root). Long jobs run
// detached (omarchy-iphone start <job>): restarting the bar never kills a backup.
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
  property int devIndex: 0
  readonly property var devices: info ? (info.devices || []) : []
  readonly property var phone: devices.length > 0 ? devices[Math.min(devIndex, devices.length - 1)] : null
  readonly property bool trusted: !!phone && phone.trusted
  readonly property string kind: phone && phone.class === "iPad" ? "iPad" : "iPhone"
  readonly property var photos: info ? info.photos : null
  readonly property var backup: info ? info.backup : null
  readonly property var drive: info ? info.drive : null
  readonly property var gphotos: info ? info.gphotos : null
  readonly property var gpThis: gphotos ? gphotos[kind.toLowerCase()] : null
  readonly property var cfg: info ? info.config : null
  readonly property var kde: info ? info.kdeconnect : null
  readonly property var kdeDevices: kde ? (kde.devices || []) : []
  readonly property var jobs: info ? (info.jobs || {}) : {}
  readonly property var scan: phone && photos && photos.scans ? photos.scans[phone.udid] : null
  readonly property var safe: phone && info && info.safe ? info.safe[phone.udid] : null
  readonly property var runningJobs: ["import", "backup", "drive", "safe"].filter(function(j) { return jobs[j] && jobs[j].running })
  readonly property bool jobRunning: runningJobs.length > 0
  readonly property var mainJob: jobRunning ? jobs[runningJobs[0]] : null
  readonly property real jobFraction: mainJob && mainJob.progress ? mainJob.progress.fraction : -1
  readonly property var jobNames: ({ "import": "Importing photos", "backup": "Backing up", "drive": "Uploading the backup to Google Drive", "safe": "Checking what is safe to delete" })
  property var wasRunning: []

  property string tab: "overview"
  property string message: ""
  property bool messageIsError: false
  property var apps: []
  property bool appsLoading: false

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
  Timer { id: messageTimer; interval: root.messageIsError ? 20000 : 9000; onTriggered: root.message = "" }

  // when a job ends, tell how it went
  onRunningJobsChanged: {
    for (var i = 0; i < wasRunning.length; i++) {
      var j = wasRunning[i]
      if (runningJobs.indexOf(j) >= 0 || !info) continue
      if (j === "import" && photos && photos.last) say(photos.last.imported + " new photos and videos" + (photos.last.failed ? " · " + photos.last.failed + " failed" : ""), photos.last.failed > 0)
      else if (j === "backup" && backup && backup.last) say(backup.last.ok ? "Backup finished" : "Backup failed: " + (backup.last.error || ""), !backup.last.ok)
      else if (j === "drive" && drive && drive.last) say(drive.last.ok ? "Backup uploaded to Google Drive" : "Upload failed: " + (drive.last.error || ""), !drive.last.ok)
      else if (j === "safe") say("Check finished: see Free up space", false)
    }
    wasRunning = runningJobs.slice()
  }

  function act(args, okText) {
    if (actionProcess.running) return
    message = ""
    actionProcess.okText = okText || ""
    actionProcess.command = [ctl].concat(args)
    actionProcess.running = true
  }
  function isRunning(j) { return !!(jobs[j] && jobs[j].running) }
  function startJob(job) { act(["start", job, phone ? phone.udid : ""], "") }
  function terminal(cmd) { if (bar) bar.run("uwsm-app -- xdg-terminal-exec bash -c " + Util.shellQuote(cmd + "; read -p 'Enter to close'")); root.close() }
  function openPath(p) { if (bar) bar.run("xdg-open " + Util.shellQuote(p)); root.close() }
  function loadApps() { if (trusted && !appsProcess.running) { appsLoading = true; appsProcess.command = [ctl, "apps", phone.udid]; appsProcess.running = true } }

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight
  onOpenedChanged: if (opened) { refresh(); Qt.callLater(function() { keyCatcher.forceActiveFocus() }) }
  onTabChanged: if (tab === "files" && apps.length === 0) loadApps()
  onDevIndexChanged: { apps = []; if (tab === "files") loadApps() }

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
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        root.appsLoading = false
        try { var r = JSON.parse(text); root.apps = r.apps || []; if (r.ok === false) root.say(r.error, true) } catch (e) {}
      }
    }
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
    // grows with the text, see the BarIconButton trap in omarchy.md
    fixedWidth: vertical ? -1 : Math.max(slotSize, Math.ceil(glyphPaintedWidth) + Style.space(10))
    readonly property string glyph: root.kind === "iPad" ? "󰓶" : "󰄜"
    text: root.jobRunning ? glyph + " " + (root.jobFraction >= 0 ? Math.round(root.jobFraction * 100) + "%" : "…")
      : root.trusted && root.phone.battery ? glyph + " " + root.phone.battery.level + "%" : glyph
    dimmed: !root.phone
    tooltipText: !root.phone ? "iPhone / iPad: not connected" : !root.trusted ? root.kind + ": tap Trust on the device"
      : (root.phone.name || root.kind) + (root.phone.battery ? " · " + root.phone.battery.level + "%" + (root.phone.battery.charging ? " charging" : "") : "")
        + (root.jobRunning ? " · " + root.jobNames[root.runningJobs[0]] : "")
    onPressed: function(code) {
      if (code === Qt.RightButton && root.trusted && !root.jobRunning) root.startJob("import")
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
    contentWidth: panel.fittedContentWidth(Style.space(450))
    contentHeight: panel.fittedContentHeight(column.implicitHeight, Style.space(720))

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
            title: root.phone && root.phone.name ? root.phone.name : "iPhone · iPad"
            meta: !root.info ? "Loading…" : !root.phone ? "Not connected · plug it in by cable"
              : !root.trusted ? (root.phone.error || "Not paired")
              : [root.phone.model, root.phone.ios ? (root.kind === "iPad" ? "iPadOS " : "iOS ") + root.phone.ios : "", root.phone.conn === "network" ? "Wi-Fi" : "USB"].filter(function(s) { return s }).join(" · ")
            foreground: root.foreground
            fontFamily: root.fontFamily
            iconComponent: Component { Text { text: root.kind === "iPad" ? "󰓶" : "󰄜"; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.display } }
            trailingControl: Component {
              PanelActionButton { iconText: "󰑐"; tooltipText: "Refresh (r)"; foreground: root.foreground; fontFamily: root.fontFamily; onClicked: root.refresh() }
            }
          }

          // several devices: pick one
          Flow {
            visible: root.devices.length > 1
            width: parent.width
            spacing: Style.space(4)
            Repeater {
              model: root.devices
              Chip {
                required property var modelData
                required property int index
                label: (modelData.class === "iPad" ? "󰓶 " : "󰄜 ") + (modelData.name || modelData.udid.slice(0, 8))
                on: root.devIndex === index
                onClicked: root.devIndex = index
              }
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

          Repeater {
            model: root.runningJobs
            Column {
              required property var modelData
              readonly property var job: root.jobs[modelData]
              width: column.width
              spacing: Style.space(4)
              Text {
                width: parent.width
                text: root.jobNames[modelData] + (job.progress && job.progress.line ? " · " + job.progress.line : "")
                color: root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
                elide: Text.ElideRight
              }
              Meter { width: parent.width; fraction: job.progress && job.progress.fraction >= 0 ? job.progress.fraction : 0 }
            }
          }

          // ============================================== Overview
          Column {
            visible: root.tab === "overview"
            width: parent.width
            spacing: Style.space(8)

            Text {
              visible: !root.phone
              width: parent.width
              text: "Connect the iPhone or iPad with its cable and unlock it. The first time, tap Trust on the device and type its passcode.\n\nWireless: Mirror (AirPlay) and Connect (KDE Connect) work over the home Wi-Fi without the cable."
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
              PanelActionButton { iconText: "󰌹"; tooltipText: "Pair (then tap Trust on the device)"; foreground: root.foreground; fontFamily: root.fontFamily
                onClicked: root.act(["pair", root.phone.udid], "Paired: the device trusts this PC") }
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
              InfoPair { label: "Storage"; value: root.trusted && root.phone.storage ? root.fmtBytes(root.phone.storage.total - root.phone.storage.free) + " used of " + root.fmtBytes(root.phone.storage.total) + " · " + root.fmtBytes(root.phone.storage.free) + " free" : "" }
              Meter { width: parent.width; fraction: root.trusted && root.phone.storage ? 1 - root.phone.storage.free / root.phone.storage.total : 0 }
            }
            InfoPair { visible: root.trusted; label: "New photos"; value: root.scan ? root.scan.pending + " (" + root.fmtBytes(root.scan.pendingBytes) + ")" : "not checked yet" }
            InfoPair { visible: root.trusted; label: "Last backup"; value: root.backup && root.backup.last ? root.ago(root.backup.last.time) + (root.backup.last.ok ? "" : " · failed") : "never" }
            InfoPair { visible: root.trusted; label: "In Google Drive"; value: root.drive && root.drive.last ? root.ago(root.drive.last.time) + (root.drive.last.ok ? "" : " · failed") : "never" }
            InfoPair {
              label: "Internet over USB"
              value: root.info && root.info.tether.connected ? "connected (" + root.info.tether.iface + ")"
                : root.info && root.info.tether.iface ? "available · turn on Personal Hotspot on the device"
                : "off"
            }
            InfoPair { label: "Screen mirroring"; value: root.info && root.info.airplay.running ? "on (\"" + root.info.airplay.name + "\")" : "off" }
            InfoPair { label: "KDE Connect"; value: !root.kde || !root.kde.installed ? "not installed"
              : root.kdeDevices.filter(function(d) { return d.requestedByPeer }).length > 0 ? "a device wants to pair (Connect tab)"
              : root.kdeDevices.filter(function(d) { return d.reachable && d.paired }).length + " connected · " + root.kdeDevices.length + " known" }
            RowLayout {
              visible: root.info && root.info.mounts && root.info.mounts.length > 0
              width: parent.width
              Text { Layout.fillWidth: true; text: root.info ? root.info.mounts.length + " device folder(s) open" : ""; color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.caption }
              PanelActionButton { iconText: "󰕓"; tooltipText: "Release the device folders (eject)"; foreground: root.foreground; fontFamily: root.fontFamily; onClicked: root.act(["eject"], "Device folders released") }
            }
          }

          // ============================================== Photos
          Column {
            visible: root.tab === "photos"
            width: parent.width
            spacing: Style.space(8)
            Text {
              width: parent.width
              text: "Photos and videos are copied as they are (HEIC, MOV) to " + (root.cfg ? root.cfg.photosDir + "/" + root.kind : "~/Pictures/" + root.kind) + "/<year>/, each one once."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
              wrapMode: Text.WordWrap
            }
            InfoPair { label: "New on the " + root.kind; value: !root.trusted ? "—" : root.scan ? root.scan.pending + " of " + root.scan.total + " · " + root.fmtBytes(root.scan.pendingBytes) + " · checked " + root.ago(root.scan.time) : "not checked yet" }
            InfoPair {
              label: "Last import"
              value: root.photos && root.photos.last ? root.ago(root.photos.last.time) + " · " + root.photos.last.imported + " new" + (root.photos.last.failed ? " · " + root.photos.last.failed + " failed" : "") : "never"
            }
            Flow {
              width: parent.width
              spacing: Style.space(6)
              Chip { label: "Import now"; visible: root.trusted && !root.isRunning("import"); onClicked: root.startJob("import") }
              Chip { label: "Check for new"; visible: root.trusted && !root.jobRunning; onClicked: root.act(["scan", root.phone.udid], "Checked") }
              Chip { label: "Open imported"; onClicked: root.openPath(root.cfg ? root.cfg.photosDir + "/" + root.kind : "") }
              Chip { label: "Browse " + root.kind; visible: root.trusted; onClicked: root.act(["open", "photos", root.phone.udid], "") }
            }
            RowLayout {
              width: parent.width
              Text { Layout.fillWidth: true; text: "Import automatically when plugged in"; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.body; wrapMode: Text.WordWrap }
              Chip { label: root.cfg && root.cfg.autoImport ? "On" : "Off"; on: !!root.cfg && root.cfg.autoImport
                onClicked: root.act(["set", "autoImport", root.cfg && root.cfg.autoImport ? "false" : "true"], "") }
            }

            PanelSeparator { foreground: root.foreground }
            PanelSectionHeader { text: "GOOGLE PHOTOS"; foreground: root.foreground; fontFamily: root.fontFamily }
            Text {
              width: parent.width
              text: !root.gphotos || !root.gphotos.installed ? "gphotos-sync is not set up (gphotos-setup)."
                : root.gpThis && root.gpThis.source ? root.gpThis.uploaded + " " + root.kind + " photos uploaded to the album \"" + root.kind + " <year>\"" + (root.gphotos.state ? " · uploader: " + root.gphotos.state : "")
                : "Imported photos are not uploaded yet."
              color: root.foreground
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
              wrapMode: Text.WordWrap
            }
            Chip { visible: !!root.gphotos && root.gphotos.installed && !(root.gpThis && root.gpThis.source); label: "Upload imported photos to Google Photos"
              onClicked: root.act(["gphotos-add"], "Added: gphotos-sync uploads them on its next pass") }

            PanelSeparator { foreground: root.foreground }
            PanelSectionHeader { text: "FREE UP SPACE ON THE " + root.kind.toUpperCase(); foreground: root.foreground; fontFamily: root.fontFamily }
            Text {
              width: parent.width
              text: "A photo is safe to delete when it is on this PC (same size; a sample compared byte by byte) and in Google Photos. Delete in the Photos app: removing files from the PC breaks the Photos library."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
              wrapMode: Text.WordWrap
            }
            Chip { label: "Check what is safe to delete"; visible: root.trusted && !root.jobRunning; onClicked: root.startJob("safe") }
            Column {
              visible: !!root.safe
              width: parent.width
              spacing: Style.space(4)
              InfoPair { label: "Safe to delete"; value: root.safe ? root.safe.safe + " of " + root.safe.total + " · " + root.fmtBytes(root.safe.safeBytes) + " · checked " + root.ago(root.safe.time) : "" }
              InfoPair { label: "Not on this PC yet"; value: root.safe ? String(root.safe.notCopied) : "" }
              InfoPair { label: "Not in Google Photos yet"; value: root.safe ? String(root.safe.notUploaded) : "" }
              InfoPair { label: "Verified byte by byte"; value: root.safe ? root.safe.verified + " (" + root.safe.mismatched + " different)" : "" }
              Text {
                width: parent.width
                text: !root.safe ? "" : root.safe.mismatched > 0 ? "⚠ Some copies differ from the originals: do not delete anything; import again and re-check."
                  : root.safe.safeUntil ? "Everything taken up to " + root.safe.safeUntil + " is safe.\nOn the " + root.kind + ": Photos › Library › All Photos › Select, drag over the photos up to " + root.safe.safeUntil + " › Delete. Then Albums › Recently Deleted › Delete All to get the space back."
                  : "Nothing is safe to delete yet: import and let gphotos-sync upload first."
                color: root.safe && root.safe.mismatched > 0 ? root.urgent : root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
                wrapMode: Text.WordWrap
              }
              Text {
                visible: !!root.safe && root.safe.icloudPhotos === "on"
                width: parent.width
                text: "⚠ iCloud Photos is on: deleting on the " + root.kind + " also deletes from iCloud and your other Apple devices. Alternative without deleting: Settings › Photos › Optimize Storage."
                color: root.urgent
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
                wrapMode: Text.WordWrap
              }
            }
          }

          // ============================================== Files
          Column {
            visible: root.tab === "files"
            width: parent.width
            spacing: Style.space(6)
            Text {
              width: parent.width
              text: !root.trusted ? "Connect and trust the device to see its files."
                : "Apps that share files (Files › On My " + root.kind + "). Opening one shows it in the file manager: copy in or out, then eject."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
              wrapMode: Text.WordWrap
            }
            ModelRow {
              visible: root.trusted
              width: parent.width
              title: "Camera roll (DCIM)"
              meta: "every photo and video"
              actions: [{ icon: "󰉋", tip: "Open in the file manager", run: function() { root.act(["open", "photos", root.phone.udid], "") } }]
            }
            Text { visible: root.appsLoading; text: "Loading apps…"; color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.bodySmall }
            Text { visible: root.trusted && !root.appsLoading && root.apps.length === 0; width: parent.width; text: "No app shares files (or the list did not load: Reload)."; color: root.dim; font.family: root.fontFamily; font.pixelSize: Style.font.bodySmall; wrapMode: Text.WordWrap }
            Repeater {
              model: root.trusted ? root.apps : []
              ModelRow {
                required property var modelData
                width: parent.width
                title: modelData.name
                meta: modelData.id + " · " + modelData.version
                actions: [{ icon: "󰉋", tip: "Open its files", run: function() { root.act(["open", "app", modelData.id, root.phone.udid], "") } }]
              }
            }
            Flow {
              visible: root.trusted
              width: parent.width
              spacing: Style.space(6)
              Chip { label: "Reload apps"; onClicked: root.loadApps() }
              Chip { label: "Eject"; onClicked: root.act(["eject"], "Device folders released") }
            }
          }

          // ============================================== Backup
          Column {
            visible: root.tab === "backup"
            width: parent.width
            spacing: Style.space(8)
            Text {
              width: parent.width
              text: "A full backup like Finder makes, in " + (root.backup ? root.backup.dir : "~/iPhoneBackup") + " (its own btrfs subvolume), then uploaded to Google Drive encrypted (restic, same password as the PC backup). The first one takes long; later ones only copy changes."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
              wrapMode: Text.WordWrap
            }
            InfoPair {
              label: "On this PC"
              value: root.backup && root.backup.last ? root.ago(root.backup.last.time) + (root.backup.last.bytes ? " · " + root.fmtBytes(root.backup.last.bytes) : "")
                + (root.backup.last.ok ? "" : " · FAILED: " + (root.backup.last.error || "")) : "never"
            }
            InfoPair {
              label: "In Google Drive"
              value: root.drive && root.drive.last ? root.ago(root.drive.last.time) + (root.drive.last.ok ? " · " + root.fmtBytes(root.drive.last.added) + " new of " + root.fmtBytes(root.drive.last.total) : " · FAILED: " + (root.drive.last.error || "")) : "never"
            }
            InfoPair {
              visible: root.trusted
              label: "Encryption"
              value: root.trusted && root.phone.backupEncrypted !== undefined ? (root.phone.backupEncrypted ? "on (passwords and Health included)" : "off (passwords and Health left out)") : "—"
            }
            Flow {
              width: parent.width
              spacing: Style.space(6)
              Chip { label: "Back up now"; visible: root.trusted && !root.isRunning("backup") && !root.isRunning("drive"); onClicked: root.startJob("backup") }
              Chip { label: "Upload to Google Drive"; visible: root.trusted && !root.isRunning("backup") && !root.isRunning("drive") && !!root.backup && !!root.backup.last && root.backup.last.ok
                onClicked: root.startJob("drive") }
              Chip { label: root.trusted && root.phone.backupEncrypted ? "Turn encryption off" : "Turn encryption on"; visible: root.trusted && !root.isRunning("backup") && !root.isRunning("import")
                onClicked: root.terminal(root.ctl + " encryption " + (root.phone.backupEncrypted ? "off" : "on") + " " + root.phone.udid) }
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
            RowLayout {
              width: parent.width
              Text { Layout.fillWidth: true; text: "Upload each backup to Google Drive"; color: root.foreground; font.family: root.fontFamily; font.pixelSize: Style.font.body; wrapMode: Text.WordWrap }
              Chip { label: root.cfg && root.cfg.driveBackup ? "On" : "Off"; on: !!root.cfg && root.cfg.driveBackup
                onClicked: root.act(["set", "driveBackup", root.cfg && root.cfg.driveBackup ? "false" : "true"], "") }
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
                onClicked: root.act(["airplay", root.info.airplay.running ? "off" : "on"], root.info.airplay.running ? "Receiver off" : "Receiver on: mirror from the device now")
              }
            }
            Text {
              width: parent.width
              text: "On the iPhone or iPad (same Wi-Fi): Control Center › Screen Mirroring › " + (root.info ? root.info.airplay.name : "Omarchy")
                + ". A window opens here with the screen and sound; stop from the device. Video and music apps can also AirPlay to it."
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
                : "Install KDE Connect from the App Store on the iPhone or iPad, open it on the same Wi-Fi and pair. Then: shared clipboard, send files and links from the Share menu, ring the device."
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
                readonly property string icon: modelData.type === "tablet" ? "󰓶 " : modelData.type === "phone" ? "󰄜 " : "󰌢 "
                title: icon + (modelData.name || modelData.id)
                meta: modelData.requestedByPeer ? "wants to pair: accept it now (the request expires in ~30 s)"
                  : modelData.requested ? "pairing requested: accept it on the device"
                  : (modelData.paired ? "paired" : "not paired") + " · " + (modelData.reachable ? "reachable" : "not reachable")
                actions: modelData.requestedByPeer ? [
                    { icon: "󰄬", tip: "Accept pairing", run: function() { root.act(["kde", "accept", modelData.id], "Paired with " + modelData.name) } },
                    { icon: "󰅖", tip: "Reject", run: function() { root.act(["kde", "reject", modelData.id], "Rejected") } }]
                  : modelData.paired
                  ? (modelData.reachable ? [
                      { icon: "󰅍", tip: "Send this PC's clipboard", run: function() { root.act(["kde", "clipboard", modelData.id], "Clipboard sent") } },
                      { icon: "󰂞", tip: "Ring the device (find it)", run: function() { root.act(["kde", "ring", modelData.id], "Ringing") } },
                      { icon: "󰍡", tip: "Ping", run: function() { root.act(["kde", "ping", modelData.id], "Ping sent") } }] : [])
                    .concat([{ icon: "󰌸", tip: "Unpair", run: function() { root.act(["kde", "unpair", modelData.id], "Unpaired") } }])
                  : modelData.reachable ? [{ icon: "󰌹", tip: "Pair (then accept on the device)", run: function() { root.act(["kde", "pair", modelData.id], "Pairing requested: accept it on " + modelData.name) } }]
                  : []
              }
            }
            Flow {
              visible: !!root.kde && root.kde.installed
              width: parent.width
              spacing: Style.space(6)
              Chip { label: "Look for devices"; onClicked: root.act(["kde", "refresh"], "Looking for devices on the Wi-Fi…") }
            }
            Text {
              visible: !!root.kde && root.kde.installed
              width: parent.width
              text: "Several phones and tablets can be paired at once. To pair: open KDE Connect on the device (same Wi-Fi), then press Pair here and accept on the device, or start it on the device and press Accept here."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
              wrapMode: Text.WordWrap
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
