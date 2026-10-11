import QtQuick
import Quickshell
import Quickshell.Io
import qs.Commons
import "../ui"

// Drivers: one clear verdict at the top, then every driver component of this
// PC (installed → available, reboot pending), DKMS, firmware (fwupd) and BIOS,
// with the actions that update them. Status comes from the Go backend
// omarchy-drivers (shares checkupdates/BIOS caches with hardwared); health and
// loaded modules still come from hardwared.
Column {
  id: tab
  property var hw: null
  spacing: Style.space(10)

  readonly property string bin: Quickshell.env("HOME") + "/.local/bin/omarchy-drivers"
  property var st: null
  property bool checking: false
  property bool showOther: false
  readonly property var d: hw ? hw.drivers : null
  readonly property var counts: d ? (d.health.counts || {}) : {}
  readonly property int fails: (counts.fail || 0) + (counts.regression || 0)

  function refresh(force) {
    if (statusProc.running) return
    checking = true
    statusProc.command = [bin, "status"].concat(force ? ["--force"] : [])
    statusProc.running = true
  }
  Component.onCompleted: refresh(false)

  Process {
    id: statusProc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: { tab.checking = false; try { tab.st = JSON.parse(text) } catch (e) {} }
    }
  }

  Line { visible: !tab.st; text: "Checking drivers, firmware and updates…"; tone: "dim" }

  // ------------------------------------------------------------ verdict
  Rectangle {
    visible: !!tab.st
    width: tab.width
    implicitHeight: verdict.implicitHeight + Style.space(18)
    radius: Style.cornerRadius
    readonly property color tint: !tab.st ? Theme.foreground : tab.st.state === "ok" ? Color.accent : Theme.urgent
    color: Qt.alpha(tint, 0.12)
    border.width: 1
    border.color: Qt.alpha(tint, 0.5)
    Row {
      id: verdict
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.verticalCenter: parent.verticalCenter
      anchors.margins: Style.space(10)
      spacing: Style.space(10)
      Text {
        text: !tab.st ? "" : tab.st.state === "ok" ? "󰄬" : tab.st.state === "reboot" ? "󰜉" : "󰚰"
        color: parent.parent.tint
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.display
      }
      Column {
        width: parent.width - Style.space(60)
        spacing: Style.space(2)
        Text {
          width: parent.width
          text: tab.st ? tab.st.summary : ""
          color: Theme.foreground
          font.family: Theme.fontFamily
          font.pixelSize: Style.font.body
          font.bold: true
          wrapMode: Text.WordWrap
        }
        Text {
          width: parent.width
          text: !tab.st ? "" : (tab.checking ? "Checking now…" : "Checked " + (tab.st.checkedAt ? Theme.ago(tab.st.checkedAt) : "never"))
            + " · " + tab.st.totalUpdates + " updates in total (" + tab.st.otherUpdates.length + " are apps)"
          color: Theme.dim
          font.family: Theme.fontFamily
          font.pixelSize: Style.font.caption
          wrapMode: Text.WordWrap
        }
      }
    }
  }
  ActionRow {
    visible: !!tab.st && tab.st.components.some(function(c) { return c.status === "update" })
    icon: "󰚰"
    label: "Update the drivers"
    hint: "with the whole system: compatibility check first, then omarchy update, then omarchy-hwcheck"
    onActivated: tab.hw.inTerminal(tab.bin + " update")
  }
  ActionRow {
    visible: !!tab.st && tab.st.firmware.length > 0
    icon: "󰍛"
    label: "Update the firmware (" + (tab.st ? tab.st.firmware.length : 0) + ")"
    hint: "fwupdmgr update; some devices finish on the next reboot"
    onActivated: tab.hw.inTerminal(tab.bin + " firmware")
  }
  ActionRow {
    visible: !!tab.st && tab.st.state === "reboot"
    icon: "󰜉"
    label: "Reboot to load the new drivers"
    hint: "the running kernel/NVIDIA module is older than the installed one"
    onActivated: tab.hw.runAndClose("omarchy system reboot")
  }
  ActionRow {
    icon: "󰑐"
    label: tab.checking ? "Checking…" : "Check again now"
    hint: "pacman mirrors, fwupd (LVFS) and DKMS"
    onActivated: tab.refresh(true)
  }

  // ------------------------------------------------------------ components
  Section { title: "DRIVERS ON THIS PC" }
  Repeater {
    model: tab.st ? tab.st.components : []
    Column {
      required property var modelData
      width: tab.width
      spacing: Style.space(1)
      Row {
        width: parent.width
        spacing: Style.space(6)
        Text {
          text: modelData.status === "ok" ? "✓" : modelData.status === "reboot" ? "↻" : "↑"
          color: modelData.status === "ok" ? Color.accent : Theme.urgent
          font.family: Theme.fontFamily
          font.pixelSize: Style.font.body
          font.bold: true
        }
        Text {
          text: modelData.name
          color: Theme.foreground
          font.family: Theme.fontFamily
          font.pixelSize: Style.font.body
          font.bold: modelData.status !== "ok"
        }
        Text {
          text: modelData.status === "ok" ? "up to date" : modelData.status === "reboot" ? "reboot pending" : "update available"
          color: modelData.status === "ok" ? Theme.dim : Theme.urgent
          font.family: Theme.fontFamily
          font.pixelSize: Style.font.caption
          anchors.baseline: parent.children[1].baseline
        }
      }
      Line { text: modelData.why; tone: "dim"; small: true }
      Repeater {
        model: modelData.packages
        Pair {
          required property var modelData
          small: true
          label: "   " + modelData.name
          value: modelData.available ? modelData.installed + " → " + modelData.available : modelData.installed
          hot: !!modelData.available
        }
      }
      Line { visible: !!modelData.note; text: modelData.note || ""; tone: "urgent"; small: true }
    }
  }
  Line {
    visible: !!tab.st
    text: tab.st ? (tab.st.dkmsOk ? "DKMS: " + tab.st.dkms.length + " modules built for this kernel (" + tab.st.dkms.map(function(l) { return l.split("/")[0] }).join(", ") + ")"
      : "DKMS: a module is not built — " + tab.st.dkms.join(" · ")) : ""
    tone: tab.st && !tab.st.dkmsOk ? "urgent" : "dim"
    small: true
  }
  Line {
    tone: "dim"
    small: true
    text: "Arch updates drivers together with the system: updating only some packages can leave the NVIDIA module built for the wrong kernel, so the button runs the whole update after a compatibility check."
  }

  // ------------------------------------------------------------ firmware
  Section { title: "FIRMWARE (fwupd / LVFS)" }
  Line {
    visible: !!tab.st && tab.st.firmware.length === 0
    text: tab.st && tab.st.firmwareError ? tab.st.firmwareError : "No firmware updates for this PC's devices (BIOS is checked below; ASUS does not publish it on LVFS)"
    tone: "dim"
    small: true
  }
  Repeater {
    model: tab.st ? tab.st.firmware : []
    Pair { required property var modelData; label: modelData.device; value: modelData.current + " → " + modelData.available; hot: true; small: true }
  }

  // ------------------------------------------------------------ BIOS
  Section { title: "BIOS · " + (tab.d ? tab.d.board.name : "") }
  Pair { visible: !!tab.d; label: "Installed"; value: tab.d ? tab.d.board.bios + " · " + tab.d.board.biosDate : "" }
  Line {
    visible: !!tab.d
    text: !tab.d ? "" : tab.d.bios.error ? "Could not check ASUS: " + tab.d.bios.error
      : tab.d.bios.newer ? "BIOS " + tab.d.bios.version + " available (" + tab.d.bios.date + ")" + (tab.d.bios.beta ? " · ASUS marks it as beta: better wait for the stable one" : "")
      : "Up to date (latest " + tab.d.bios.version + ", " + tab.d.bios.date + ")"
    tone: tab.d && tab.d.bios.newer ? "normal" : "dim"
    bold: !!tab.d && tab.d.bios.newer
  }
  ActionRow {
    visible: !!tab.d && tab.d.bios.newer
    icon: "󰖟"
    label: "Open the ASUS BIOS page"
    hint: "flash it from the BIOS with EZ Flash after a snapshot; never from Linux"
    onActivated: tab.hw.runAndClose("xdg-open " + tab.hw.bar.shellQuote(tab.d.bios.page))
  }

  // ------------------------------------------------------------ other updates
  Section { title: "OTHER UPDATES (APPS, NOT DRIVERS)" }
  ActionRow {
    visible: !!tab.st && tab.st.otherUpdates.length > 0
    icon: tab.showOther ? "󰅃" : "󰅀"
    label: (tab.st ? tab.st.otherUpdates.length : 0) + " apps can be updated"
    hint: tab.showOther ? "hide" : "show the list"
    onActivated: tab.showOther = !tab.showOther
  }
  Repeater {
    model: tab.showOther && tab.st ? tab.st.otherUpdates : []
    Pair { required property var modelData; small: true; label: modelData.name; value: modelData.installed + " → " + modelData.available }
  }
  ActionRow {
    visible: !!tab.st && tab.st.totalUpdates > 0
    icon: "󰚰"
    label: "Update everything"
    hint: "same as above: compatibility check + omarchy update"
    onActivated: tab.hw.inTerminal(tab.bin + " update")
  }

  // ------------------------------------------------------------ health
  Section { title: "HEALTH (omarchy-hwcheck)" }
  Line {
    visible: !!tab.d
    text: tab.d && tab.d.health.error ? "omarchy-hwcheck: " + tab.d.health.error
      : (tab.counts.ok || 0) + " OK · " + (tab.counts.info || 0) + " info · " + (tab.counts.warn || 0) + " warnings · " + tab.fails + " failures"
    tone: tab.fails > 0 ? "urgent" : "normal"
  }
  Repeater {
    model: tab.d ? tab.d.health.issues : []
    Column {
      required property var modelData
      width: tab.width
      spacing: Style.space(2)
      Line { text: (modelData.level === "warn" ? "⚠ " : "✖ ") + modelData.id + "  " + modelData.title; tone: modelData.level === "warn" ? "normal" : "urgent" }
      Line { visible: modelData.details.length > 0; text: modelData.details.join("\n"); tone: "dim"; small: true }
    }
  }
  ActionRow {
    icon: ""
    label: "Full report in a terminal"
    hint: "omarchy-hwcheck · omarchy-hwcheck diff"
    onActivated: tab.hw.inTerminal("omarchy-hwcheck; omarchy-hwcheck diff")
  }

  // ------------------------------------------------------------ loaded
  Section { title: "LOADED DRIVERS" }
  Repeater {
    model: tab.d ? tab.d.loaded : []
    Pair { required property var modelData; label: modelData.label; value: modelData.version }
  }
}
