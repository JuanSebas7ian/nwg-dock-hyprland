import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import qs.Commons
import qs.Ui
import "../ui"

// Live state: on/off, RAM, pressure on work, VRAM (with the hour's p95/p99),
// the level and why, what the guard is doing right now, and a self-check.
Column {
  id: tab
  property var mg: null
  spacing: Style.space(8)

  readonly property var st: mg ? mg.st : null
  readonly property var ram: mg ? mg.ram : null
  readonly property var vram: mg ? mg.vram : null

  Notice {
    visible: !!tab.mg && !!tab.mg.ui && !tab.mg.running
    width: parent.width
    level: "crit"
    text: "omarchy-memguard is not running (" + (tab.mg && tab.mg.ui ? tab.mg.ui.service : "?") + "). Nothing watches RAM and VRAM."
  }
  ActionRow {
    visible: !!tab.mg && !!tab.mg.ui && !tab.mg.running
    icon: "󰐊"
    label: "Start it"
    hint: "systemctl --user restart omarchy-memguard"
    onActivated: tab.mg.startService()
  }

  RowLayout {
    width: parent.width
    ColumnLayout {
      Layout.fillWidth: true
      spacing: 0
      Text { text: "Act on spikes"; color: Theme.foreground; font.family: Theme.fontFamily; font.pixelSize: Style.font.bodySmall }
      Text {
        Layout.fillWidth: true
        text: !tab.mg ? "" : tab.mg.enabled ? "On: pauses, caps and unloads only when the machine is pushed to its limit"
                                             : "Off: watching only, nothing is touched"
        color: Theme.dim
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.caption
        wrapMode: Text.WordWrap
      }
    }
    ToggleSwitch {
      checked: !!tab.mg && tab.mg.enabled
      busy: !!tab.mg && tab.mg.busy === "enabled"
      onToggled: tab.mg.setValue("enabled", tab.mg.enabled ? 0 : 1)
    }
  }

  Section { title: "NOW" }
  Gauge {
    width: parent.width
    label: "RAM"
    value: tab.ram ? tab.mg.bytes(tab.ram.used) + " of " + tab.mg.bytes(tab.ram.total) + " · " + tab.ram.availPct + "% free" : "–"
    fraction: tab.ram && tab.ram.total > 0 ? tab.ram.used / tab.ram.total : 0
    hot: !!tab.mg && tab.mg.level >= 2
  }
  Pair {
    small: true
    label: "Last hour"
    value: tab.ram ? "p95 " + tab.mg.bytes(tab.ram.p95) + " · p99 " + tab.mg.bytes(tab.ram.p99) : ""
  }
  Gauge {
    width: parent.width
    label: "Work waiting for memory"
    value: tab.ram ? tab.ram.psiSome + "%" + (tab.ram.tte ? " · full in " + Math.round(tab.ram.tte) + " s" : "") : "–"
    fraction: tab.ram ? Math.min(1, (tab.ram.psiSome || 0) / 50) : 0
    hot: !!tab.ram && tab.ram.psiSome > (tab.mg.cfg ? tab.mg.cfg.psiWarn : 10)
  }
  Pair {
    small: true
    label: "Swap"
    value: tab.ram ? tab.ram.swapPct + "% used" : ""
  }
  Gauge {
    visible: !!tab.vram
    width: parent.width
    label: "VRAM"
    value: tab.vram ? tab.mg.bytes(tab.vram.used) + " of " + tab.mg.bytes(tab.vram.total) + " · " + tab.mg.bytes(tab.vram.free) + " free" : ""
    fraction: tab.vram && tab.vram.total > 0 ? tab.vram.used / tab.vram.total : 0
    hot: !!tab.mg && tab.mg.vlevel >= 2
  }
  Pair {
    visible: !!tab.vram
    small: true
    label: "Last hour"
    value: tab.vram ? "p95 " + tab.mg.bytes(tab.vram.p95) + " · p99 " + tab.mg.bytes(tab.vram.p99) : ""
  }

  Section { title: "WHAT IT IS DOING" }
  Line {
    text: !tab.st ? "–"
      : !tab.mg.enabled ? "Nothing: switched off."
      : tab.mg.level === 0 && tab.mg.vlevel === 0 && !tab.mg.acting ? "Nothing: the machine is calm."
      : "Level " + tab.mg.level + (tab.st.reasons && tab.st.reasons.length ? ": " + tab.st.reasons.join("; ") : "")
    tone: !!tab.mg && tab.mg.level >= 2 ? "urgent" : "normal"
  }
  Repeater {
    model: tab.st ? (tab.st.frozen || []) : []
    Pair { required property var modelData; label: "⏸ Paused"; value: modelData }
  }
  Repeater {
    model: tab.st ? (tab.st.throttled || []) : []
    Pair { required property var modelData; label: "󰾆 Capped (still running)"; value: modelData; hot: true }
  }
  Repeater {
    model: tab.st ? (tab.st.unloaded || []) : []
    Pair { required property var modelData; label: "Ollama model unloaded"; value: modelData; small: true }
  }
  Pair {
    visible: !!tab.st && !!tab.st.dictation
    label: "Dictation model"
    value: tab.st ? tab.st.dictation : ""
  }
  Pair {
    label: "Protected apps"
    value: tab.st ? String((tab.st.protected || []).length) : ""
  }

  Section { title: "CHECK" }
  ActionRow {
    icon: "󰄬"
    label: "Self-check"
    hint: "omarchy-memguard smoke: sensors, cgroups, NVML, service"
    onActivated: tab.mg.selfCheck()
  }
  Repeater {
    model: tab.mg ? tab.mg.smoke : []
    Pair {
      required property var modelData
      small: true
      label: modelData.text
      value: modelData.status
      hot: modelData.status === "FAIL"
    }
  }
}
