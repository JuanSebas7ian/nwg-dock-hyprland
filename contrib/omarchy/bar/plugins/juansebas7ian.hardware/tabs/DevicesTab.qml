import QtQuick
import QtQuick.Layouts
import qs.Commons
import qs.Ui
import "../ui"

// Devices: Bluetooth (battery, connect, reconnect at boot, visibility),
// wireless batteries, keyboards / pointers / game controllers, audio,
// cameras and the USB tree with this boot's errors per port.
Column {
  id: tab
  property var hw: null
  spacing: Style.space(8)

  readonly property var p: hw ? hw.devices : null
  readonly property var bt: p ? p.bluetooth : null
  readonly property var adapter: bt ? bt.adapter : null
  function kindIcon(icon) {
    if (!icon) return "󰂯"
    if (icon.indexOf("audio") === 0) return "󰓃"
    if (icon.indexOf("input-keyboard") === 0) return "󰌌"
    if (icon.indexOf("input-mouse") === 0) return "󰍽"
    if (icon.indexOf("input-gaming") === 0) return "󰊴"
    if (icon.indexOf("phone") === 0) return "󰏲"
    return "󰂯"
  }

  Line { visible: !tab.p; text: "Looking at devices…"; tone: "dim" }
  Repeater {
    model: tab.hw ? tab.hw.alertsFor("devices") : []
    Notice { required property var modelData; level: modelData.level; text: modelData.title + " · " + modelData.detail }
  }

  // ------------------------------------------------------------ Bluetooth
  Section { title: "BLUETOOTH" }
  Pair {
    visible: !!tab.bt
    label: tab.adapter ? tab.adapter.name + " · " + tab.adapter.address : "Adapter"
    value: !tab.bt ? "" : tab.bt.blocked ? "off (Omarchy switch)" : !tab.adapter ? "not found" : tab.adapter.powered ? "on" : "powering on…"
    hot: !!tab.bt && !tab.adapter && !tab.bt.blocked
    strong: true
  }
  Pair {
    visible: !!tab.bt
    small: true
    label: "Boot: power on + reconnect"
    value: tab.bt ? (tab.bt.service === "active" ? "active · tries at " + tab.bt.settings.reconnectAfter.split(" ").join("/") + " s" : tab.bt.service || "not installed") : ""
    hot: !!tab.bt && tab.bt.service !== "active"
  }
  RowLayout {
    visible: !!tab.adapter
    width: parent.width
    ColumnLayout {
      Layout.fillWidth: true
      spacing: 0
      Text { text: "Visible to other devices"; color: Theme.foreground; font.family: Theme.fontFamily; font.pixelSize: Style.font.bodySmall }
      Text {
        Layout.fillWidth: true
        text: tab.adapter && tab.adapter.discoverable
          ? "Anyone nearby can pair without confirmation (Omarchy's agent accepts all). Turn off unless pairing."
          : "Hidden. Paired devices still reconnect. Turn on only to pair something new."
        color: tab.adapter && tab.adapter.discoverable ? Theme.urgent : Theme.dim
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.caption
        wrapMode: Text.WordWrap
      }
    }
    ToggleSwitch {
      checked: !!tab.adapter && tab.adapter.discoverable
      busy: !!tab.hw && tab.hw.pendingBt["adapter"] !== undefined
      onToggled: tab.hw.bt("visible", null, !checked)
    }
  }
  Repeater {
    model: tab.bt ? tab.bt.devices : []
    Column {
      id: dev
      required property var modelData
      width: tab.width
      spacing: Style.space(2)
      readonly property bool busy: tab.hw.pendingBt[modelData.mac] !== undefined
      RowLayout {
        width: parent.width
        spacing: Style.space(6)
        Text { text: tab.kindIcon(dev.modelData.icon); color: Theme.foreground; opacity: dev.modelData.connected ? 1 : 0.5; font.family: Theme.fontFamily; font.pixelSize: Style.font.icon }
        ColumnLayout {
          Layout.fillWidth: true
          spacing: 0
          Text {
            Layout.fillWidth: true
            text: dev.modelData.name + (dev.modelData.battery !== null ? "  󰁹 " + dev.modelData.battery + "%" : "")
            textFormat: Text.PlainText
            color: dev.modelData.battery !== null && dev.modelData.battery <= 15 ? Theme.urgent : Theme.foreground
            font.family: Theme.fontFamily
            font.pixelSize: Style.font.bodySmall
            font.bold: dev.modelData.connected
            elide: Text.ElideRight
          }
          Text {
            text: (dev.busy ? (tab.hw.pendingBt[dev.modelData.mac] === "connect" ? "connecting…" : "working…")
                    : dev.modelData.connected ? "connected" : "not connected")
              + " · " + (dev.modelData.autoconnect ? "reconnects at boot" : "manual")
            color: Theme.dim
            font.family: Theme.fontFamily
            font.pixelSize: Style.font.caption
          }
        }
        PanelActionButton {
          iconText: dev.modelData.connected ? "󰂲" : "󰂱"
          tooltipText: dev.modelData.connected ? "Disconnect" : "Connect"
          enabled: !dev.busy
          foreground: Theme.foreground
          fontFamily: Theme.fontFamily
          onClicked: tab.hw.bt(dev.modelData.connected ? "disconnect" : "connect", dev.modelData.mac)
        }
        ToggleSwitch {
          checked: dev.modelData.autoconnect
          busy: dev.busy
          onToggled: tab.hw.bt("autoconnect", dev.modelData.mac, !checked)
        }
      }
    }
  }
  Caption { visible: !!tab.bt && tab.bt.devices.length > 0; text: "Switch = reconnect this device when the PC starts." }
  ActionRow {
    icon: "󰂰"
    label: "Pair a new device"
    hint: "Opens Omarchy's Bluetooth panel (scan and pair)"
    onActivated: tab.hw.runAndClose("omarchy-shell omarchy.bluetooth open")
  }

  // ------------------------------------------------------------ batteries
  Section { visible: !!tab.p && tab.p.batteries.length > 0; title: "WIRELESS BATTERIES" }
  Repeater {
    model: tab.p ? tab.p.batteries : []
    Column {
      required property var modelData
      width: tab.width
      spacing: Style.space(2)
      Pair {
        label: "󰁹 " + modelData.name + (modelData.source === "solaar" ? " · Logitech" : "")
        value: modelData.percent !== null ? modelData.percent + "%" + (modelData.status ? " · " + modelData.status : "") : (modelData.level || "not connected")
        hot: modelData.percent !== null && modelData.percent <= 15
      }
      Meter { visible: modelData.percent !== null; fraction: (modelData.percent || 0) / 100; hot: modelData.percent !== null && modelData.percent <= 15; thickness: Style.space(3) }
    }
  }

  // ------------------------------------------------------------ input
  Section { visible: !!tab.p; title: "INPUT" }
  Repeater {
    model: tab.p ? tab.p.input : []
    Pair {
      required property var modelData
      label: (modelData.kind === "gamepad" ? "󰊴 " : modelData.kind === "pointer" ? "󰍽 " : "󰌌 ") + modelData.name
      value: (modelData.kinds.length > 1 ? modelData.kinds.join(" + ") + " · " : "")
        + (modelData.virtual ? "virtual (pad-keepalive)" : modelData.phys.indexOf("usb-") === 0 ? "USB" : modelData.phys ? modelData.phys.split("/")[0] : "")
      small: modelData.virtual
    }
  }
  Pair {
    visible: !!tab.p && tab.p.input.some(function(d) { return d.kind === "gamepad" })
    small: true
    label: "pad-keepalive (keeps the Xbox pad alive in games)"
    value: tab.p ? tab.p.padKeepalive : ""
    hot: !!tab.p && tab.p.padKeepalive !== "active"
  }

  // ------------------------------------------------------------ audio & video
  Section { visible: !!tab.p; title: "AUDIO · CAMERAS" }
  Pair { visible: !!tab.p; label: "Output"; value: tab.p ? tab.p.audio.output || "–" : "" }
  Pair { visible: !!tab.p; label: "Input"; value: tab.p ? tab.p.audio.input || "none" : "" }
  Repeater {
    model: tab.p ? tab.p.audio.disabled : []
    Pair { required property var modelData; small: true; label: "Disabled in WirePlumber"; value: modelData }
  }
  Repeater {
    model: tab.p ? tab.p.cameras : []
    Pair { required property var modelData; label: "󰄀 " + modelData; value: "video" }
  }
  ActionRow {
    icon: "󰕾"
    label: "Change audio output or input"
    hint: "Opens Omarchy's audio panel"
    onActivated: tab.hw.runAndClose("omarchy-shell omarchy.audio open")
  }

  // ------------------------------------------------------------ USB
  Section { visible: !!tab.p; title: "USB" }
  Repeater {
    model: tab.p ? tab.p.usb : []
    Column {
      required property var modelData
      width: tab.width
      Pair {
        label: "  ".repeat(modelData.depth - 1) + (modelData.hub ? "󰇓 " : "└ ") + modelData.name + "  " + modelData.port
        value: modelData.speed + (modelData.drivers.length > 0 ? " · " + modelData.drivers.join(", ") : " · no driver")
        hot: !!modelData.errors || (!modelData.hub && modelData.drivers.length === 0)
        small: true
      }
      Caption {
        visible: !!modelData.errors
        leftPadding: Style.space(12)
        color: Theme.urgent
        text: modelData.errors ? modelData.errors.count + " errors this boot · last: " + modelData.errors.last : ""
      }
    }
  }
  Caption { visible: !!tab.p; text: tab.p ? "Updated " + Theme.ago(tab.p.checkedAt) + " · refreshes every 3 s while this tab is open" : "" }
}
