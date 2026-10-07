import QtQuick
import qs.Commons
import "../ui"

// Drivers: BIOS against ASUS, omarchy-hwcheck health, pending driver and
// GPU updates (one checkupdates call, cached an hour), loaded drivers.
Column {
  id: tab
  property var hw: null
  spacing: Style.space(10)

  readonly property var d: hw ? hw.drivers : null
  readonly property var counts: d ? (d.health.counts || {}) : {}
  readonly property int fails: (counts.fail || 0) + (counts.regression || 0)
  readonly property var ups: d ? d.updates : null

  Line { visible: !tab.d; text: "Checking drivers, updates and BIOS… (the first check after login waits 20 s)"; tone: "dim" }

  // ------------------------------------------------------------ BIOS
  Section { title: "BIOS · " + (tab.d ? tab.d.board.name : "") }
  Pair { visible: !!tab.d; label: "Installed"; value: tab.d ? tab.d.board.bios + " · " + tab.d.board.biosDate : "" }
  Line {
    visible: !!tab.d
    text: !tab.d ? "" : tab.d.bios.error ? "Could not check ASUS: " + tab.d.bios.error
      : tab.d.bios.newer ? "BIOS " + tab.d.bios.version + " available (" + tab.d.bios.date + ")" + (tab.d.bios.beta ? " · ASUS marks it as beta" : "")
      : "Up to date (latest " + tab.d.bios.version + ", " + tab.d.bios.date + ")"
    tone: tab.d && tab.d.bios.newer ? "normal" : "dim"
    bold: !!tab.d && tab.d.bios.newer
  }
  Line { visible: !!tab.d && tab.d.bios.newer && !!tab.d.bios.notes; text: tab.d && tab.d.bios.notes ? tab.d.bios.notes : ""; tone: "dim"; small: true }
  ActionRow {
    visible: !!tab.d && tab.d.bios.newer
    icon: "󰖟"
    label: "Open the ASUS BIOS page"
    hint: "Flash it from the BIOS with EZ Flash, after a snapshot; not from Linux"
    onActivated: tab.hw.runAndClose("xdg-open " + tab.hw.bar.shellQuote(tab.d.bios.page))
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

  // ------------------------------------------------------------ updates
  Section { title: "UPDATES" }
  Line {
    visible: !!tab.ups
    text: !tab.ups ? "" : tab.ups.error && !tab.ups.checkedAt ? "Could not check: " + tab.ups.error
      : (tab.ups.drivers.length === 0 ? "Drivers up to date" : tab.ups.drivers.length + " driver packages to update")
        + " · " + tab.ups.total + " updates in total" + (tab.ups.checkedAt ? " · checked " + Theme.ago(tab.ups.checkedAt) : "")
        + (tab.ups.stale ? " (offline: last answer)" : "")
    tone: tab.ups && tab.ups.drivers.length > 0 ? "normal" : "dim"
  }
  Repeater {
    model: tab.ups ? tab.ups.drivers : []
    Pair { required property var modelData; label: modelData.name; value: modelData.from + " → " + modelData.to; small: true }
  }
  Line {
    visible: !!tab.ups && tab.ups.drivers.some(function(p) { return p.name === "linux" || p.name.indexOf("nvidia") === 0 })
    text: "After updating, omarchy-hwcheck checks that the NVIDIA module was rebuilt before you reboot."
    tone: "dim"
    small: true
  }
  ActionRow {
    visible: !!tab.ups && tab.ups.total > 0
    icon: "󰚰"
    label: "Update the system"
    hint: "omarchy update (validates drivers before rebooting)"
    onActivated: tab.hw.inTerminal("omarchy-update")
  }

  // ------------------------------------------------------------ loaded
  Section { title: "LOADED DRIVERS" }
  Repeater {
    model: tab.d ? tab.d.loaded : []
    Pair { required property var modelData; label: modelData.label; value: modelData.version }
  }
  Caption { visible: !!tab.d; text: tab.d ? "Checked " + Theme.ago(tab.d.checkedAt) + " · r = check again now (network)" : "" }
}
