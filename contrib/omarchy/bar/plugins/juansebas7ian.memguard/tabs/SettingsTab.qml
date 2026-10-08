import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import qs.Commons
import qs.Ui
import "../ui"

// Every setting from the daemon's own schema, grouped: switches for on/off
// values, − / + steppers within the allowed range, ↺ back to the default.
Column {
  id: tab
  property var mg: null
  spacing: Style.space(6)

  readonly property var schema: mg && mg.ui && mg.ui.config ? mg.ui.config.schema : []
  readonly property var groups: {
    var seen = []
    for (var i = 0; i < schema.length; i++) if (seen.indexOf(schema[i].group) < 0) seen.push(schema[i].group)
    return seen
  }
  function fmt(v, unit) {
    if (unit === "bool") return v ? "on" : "off"
    return (Math.round(v * 10) / 10) + " " + unit
  }
  function step(row, dir) {
    var v = Math.max(row.min, Math.min(row.max, Math.round((row.value + dir * row.step) * 100) / 100))
    if (v !== row.value) tab.mg.setValue(row.key, v)
  }

  Line {
    small: true
    tone: "dim"
    text: "Changes apply at once (the guard reloads them, no restart). Thresholds decide when it acts; it never acts on high use alone."
  }

  Repeater {
    model: tab.groups
    Column {
      id: group
      required property var modelData
      width: tab.width
      spacing: Style.space(4)
      Section { title: String(group.modelData).toUpperCase() }
      Repeater {
        model: tab.schema.filter(function(r) { return r.group === group.modelData })
        RowLayout {
          id: row
          required property var modelData
          width: tab.width
          spacing: Style.space(4)
          readonly property bool changed: modelData.value !== modelData.default
          readonly property bool isBool: modelData.unit === "bool"
          ColumnLayout {
            Layout.fillWidth: true
            spacing: 0
            Text {
              Layout.fillWidth: true
              text: row.modelData.label
              color: Theme.foreground
              font.family: Theme.fontFamily
              font.pixelSize: Style.font.bodySmall
              wrapMode: Text.WordWrap
            }
            Text {
              Layout.fillWidth: true
              text: (row.changed ? "yours · default " + tab.fmt(row.modelData.default, row.modelData.unit) : "default")
                + (row.isBool ? "" : " · " + row.modelData.min + "–" + row.modelData.max + " " + row.modelData.unit)
              color: Theme.dim
              font.family: Theme.fontFamily
              font.pixelSize: Style.font.caption
            }
          }
          ToggleSwitch {
            visible: row.isBool
            checked: row.isBool && row.modelData.value > 0
            busy: !!tab.mg && tab.mg.busy === row.modelData.key
            onToggled: tab.mg.setValue(row.modelData.key, row.modelData.value > 0 ? 0 : 1)
          }
          PanelActionButton {
            visible: !row.isBool
            iconText: "󰍴"
            tooltipText: "− " + row.modelData.step + " " + row.modelData.unit
            foreground: Theme.foreground
            fontFamily: Theme.fontFamily
            enabled: !!tab.mg && tab.mg.busy === "" && row.modelData.value > row.modelData.min
            onClicked: tab.step(row.modelData, -1)
          }
          Text {
            visible: !row.isBool
            Layout.preferredWidth: Style.space(64)
            horizontalAlignment: Text.AlignHCenter
            text: tab.fmt(row.modelData.value, row.modelData.unit)
            color: row.changed ? Theme.foreground : Theme.dim
            font.family: Theme.fontFamily
            font.pixelSize: Style.font.bodySmall
            font.bold: row.changed
          }
          PanelActionButton {
            visible: !row.isBool
            iconText: "󰐕"
            tooltipText: "+ " + row.modelData.step + " " + row.modelData.unit
            foreground: Theme.foreground
            fontFamily: Theme.fontFamily
            enabled: !!tab.mg && tab.mg.busy === "" && row.modelData.value < row.modelData.max
            onClicked: tab.step(row.modelData, 1)
          }
          PanelActionButton {
            iconText: "󰦛"
            tooltipText: "Back to the default"
            opacity: row.changed ? 1 : 0.25
            foreground: Theme.foreground
            fontFamily: Theme.fontFamily
            enabled: row.changed && !!tab.mg && tab.mg.busy === ""
            onClicked: tab.mg.change("reset", row.modelData.key, null, row.modelData.key)
          }
        }
      }
    }
  }

  Section { title: "ALL" }
  ActionRow {
    icon: "󰦛"
    label: "Reset every setting"
    hint: "Exceptions (Apps tab) are kept"
    onActivated: tab.mg.change("reset", null, null, "all")
  }
}
