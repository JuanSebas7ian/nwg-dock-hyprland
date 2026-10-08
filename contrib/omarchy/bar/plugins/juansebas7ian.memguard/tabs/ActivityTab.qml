import QtQuick
import Quickshell
import qs.Commons
import qs.Ui
import "../ui"

// What the guard did, newest first (events.log).
Column {
  id: tab
  property var mg: null
  spacing: Style.space(6)

  readonly property var events: mg && mg.ui ? (mg.ui.events || []) : []

  Line {
    visible: tab.events.length === 0
    tone: "dim"
    text: "Nothing yet: it has not needed to act."
  }
  Repeater {
    model: tab.events
    Column {
      required property var modelData
      width: tab.width
      spacing: 0
      Text {
        text: modelData.when
        color: Theme.dim
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.caption
      }
      Line {
        text: modelData.text
        tone: /slowed|capped|level 3|exhausted/.test(modelData.text) ? "urgent" : "normal"
      }
    }
  }
  ActionRow {
    icon: "󰈙"
    label: "Open the full log"
    hint: "~/.local/state/omarchy-memguard/events.log"
    onActivated: tab.mg.openLog()
  }
}
